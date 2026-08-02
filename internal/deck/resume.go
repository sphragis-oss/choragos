// SPDX-License-Identifier: Apache-2.0

// Session snapshot: deck state persisted across quits, restored by serve --resume.
package deck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/pane"
	"github.com/sphragis-oss/choragos/internal/wire"
)

// snapshotVersion gates session.json across deck versions; unknown versions refuse resume.
const snapshotVersion = 1

// snapshotName is the deck-state record under contextDir.
const snapshotName = "session.json"

// Snapshot is the deck state written on board and gate changes and on quit.
type Snapshot struct {
	Version    int         `json:"version"`
	Saved      time.Time   `json:"saved"`
	ConfigPath string      `json:"config_path,omitempty"`
	TaskSeq    int         `json:"task_seq"`
	Roster     []SnapRole  `json:"roster"`
	Board      []wire.Task `json:"board,omitempty"`
	Gates      []wire.Gate `json:"gates,omitempty"`
	Layout     []byte      `json:"layout,omitempty"`
}

// SnapRole preserves one roster slot; order and tombstones are index-load-bearing.
type SnapRole struct {
	Name string `json:"name"`
	Gone bool   `json:"gone,omitempty"`
}

// saveSnapshot persists the deck state atomically; best-effort, failures only log.
func (s *session) saveSnapshot() {
	roster := make([]SnapRole, 0, len(s.panes))
	for _, e := range s.panes {
		roster = append(roster, SnapRole{Name: e.role.Name, Gone: e.gone})
	}
	snap := Snapshot{
		Version: snapshotVersion, Saved: time.Now(), ConfigPath: s.cfg.Path,
		TaskSeq: s.taskSeq, Roster: roster,
		Board: toWireTasks(s.board), Gates: toWireGates(s.gates), Layout: s.layout,
	}
	b, _ := json.Marshal(&snap) // plain structs: cannot fail
	path := filepath.Join(contextDir, snapshotName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		s.log().Warn("session snapshot failed", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		s.log().Warn("session snapshot failed", "err", err)
	}
}

// LoadSnapshot reads .choragos/session.json and validates it against cfg for --resume.
func LoadSnapshot(cfg config.Config) (*Snapshot, error) {
	path := filepath.Join(contextDir, snapshotName)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no session snapshot to resume at %s", path)
	}
	var snap Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, fmt.Errorf("corrupt session snapshot %s: %w", path, err)
	}
	if snap.Version != snapshotVersion {
		return nil, fmt.Errorf("session snapshot is v%d, this deck writes v%d; start without --resume", snap.Version, snapshotVersion)
	}
	if snap.ConfigPath != cfg.Path {
		return nil, fmt.Errorf("snapshot was taken with config %q, not %q; resume with the same config or start without --resume", snap.ConfigPath, cfg.Path)
	}
	have := make(map[string]bool, len(cfg.Roles))
	for _, r := range cfg.Roles {
		have[r.Name] = true
	}
	for _, sr := range snap.Roster {
		if !sr.Gone && !have[sr.Name] {
			return nil, fmt.Errorf("config no longer defines role %q from the snapshot; restore it or start without --resume", sr.Name)
		}
	}
	return &snap, nil
}

// spawnResume spawns the roster in snapshot order, tombstones kept so stored indices stay valid;
// config roles the snapshot never knew spawn appended, like a reload add.
func spawnResume(cfg config.Config, snap *Snapshot, cols, rows int, socket, baseURL string) ([]*entry, error) {
	byName := make(map[string]config.Role, len(cfg.Roles))
	for _, r := range cfg.Roles {
		byName[r.Name] = r
	}
	var entries []*entry
	fail := func(err error) ([]*entry, error) {
		for _, e := range entries {
			_ = e.pane.Close()
		}
		return nil, err
	}
	seen := make(map[string]bool, len(snap.Roster))
	for _, sr := range snap.Roster {
		seen[sr.Name] = true
		if sr.Gone {
			entries = append(entries, tombstoneEntry(sr.Name))
			continue
		}
		r := byName[sr.Name] // presence validated by LoadSnapshot
		p, err := startRole(r, cols, rows, roleEnv(r, socket, baseURL))
		if err != nil {
			return fail(err)
		}
		entries = append(entries, &entry{role: r, pane: p})
	}
	for _, r := range cfg.Roles {
		if seen[r.Name] {
			continue
		}
		p, err := startRole(r, cols, rows, roleEnv(r, socket, baseURL))
		if err != nil {
			return fail(err)
		}
		entries = append(entries, &entry{role: r, pane: p})
	}
	return entries, nil
}

// tombstoneEntry rebuilds a reload-removed role's slot: no process, index kept valid.
func tombstoneEntry(name string) *entry {
	p := pane.Remote(80, 24, func([]byte) error { return pane.ErrPaneClosed }, func(int, int) {})
	return &entry{role: config.Role{Name: name}, pane: p, gone: true, exited: true}
}

// applyResume restores the deck state from the snapshot after the roster spawned.
func (s *session) applyResume(snap *Snapshot) {
	s.taskSeq = snap.TaskSeq
	s.board = fromWireTasks(snap.Board)
	s.gates = fromWireGates(snap.Gates)
	s.layout = snap.Layout
	s.log().Info("session resumed", "saved", snap.Saved.Format(time.RFC3339), "task_seq", s.taskSeq, "board", len(s.board), "gates", len(s.gates))
}
