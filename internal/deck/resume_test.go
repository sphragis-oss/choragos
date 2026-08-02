// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
	"github.com/sphragis-oss/choragos/internal/wm"
)

func TestSnapshotRoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())
	s := &session{
		cfg:     config.Config{Path: ".choragos.toml", Roles: []config.Role{{Name: "orc", Start: true}, {Name: "coder"}}},
		taskSeq: 7,
		panes: []*entry{
			{role: config.Role{Name: "orc", Start: true}},
			{role: config.Role{Name: "old"}, gone: true},
			{role: config.Role{Name: "coder"}},
		},
		board: []taskEvent{
			{at: time.Now().Add(-time.Minute), kind: "delegate", id: "T7", to: "coder", task: "fix it"},
		},
		gates:  []pendingGate{{cmd: ipc.Command{Cmd: "delegate", Task: "risky"}, to: "coder", at: time.Now()}},
		layout: wm.New(0).Marshal(),
	}
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot()
	snap, err := LoadSnapshot(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snap.TaskSeq != 7 || len(snap.Board) != 1 || len(snap.Gates) != 1 || len(snap.Layout) == 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	wantRoster := []SnapRole{{Name: "orc"}, {Name: "old", Gone: true}, {Name: "coder"}}
	for i, want := range wantRoster {
		if snap.Roster[i] != want {
			t.Errorf("roster[%d] = %+v, want %+v", i, snap.Roster[i], want)
		}
	}
	if snap.Board[0].ID != "T7" || snap.Gates[0].To != "coder" {
		t.Fatalf("board/gates = %+v / %+v", snap.Board, snap.Gates)
	}

	restored := &session{}
	restored.applyResume(snap)
	if restored.taskSeq != 7 || len(restored.board) != 1 || len(restored.gates) != 1 {
		t.Fatalf("applyResume: seq=%d board=%d gates=%d", restored.taskSeq, len(restored.board), len(restored.gates))
	}
	if restored.board[0].id != "T7" || !restored.board[0].doneAt.IsZero() {
		t.Fatalf("restored board row = %+v", restored.board[0])
	}
	if !strings.Contains(restored.recapNote(), "T7 -> coder") {
		t.Fatalf("recap should list the restored in-flight task:\n%s", restored.recapNote())
	}
}

func TestLoadSnapshotRefusals(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Config{Path: "team.toml", Roles: []config.Role{{Name: "orc", Start: true}}}
	if _, err := LoadSnapshot(cfg); err == nil || !strings.Contains(err.Error(), "no session snapshot") {
		t.Fatalf("missing snapshot: %v", err)
	}
	path := filepath.Join(contextDir, snapshotName)
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(cfg); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt snapshot: %v", err)
	}
	write(Snapshot{Version: 99, ConfigPath: "team.toml"})
	if _, err := LoadSnapshot(cfg); err == nil || !strings.Contains(err.Error(), "v99") {
		t.Fatalf("version gate: %v", err)
	}
	write(Snapshot{Version: snapshotVersion, ConfigPath: "other.toml"})
	if _, err := LoadSnapshot(cfg); err == nil || !strings.Contains(err.Error(), "other.toml") {
		t.Fatalf("config path gate: %v", err)
	}
	write(Snapshot{Version: snapshotVersion, ConfigPath: "team.toml",
		Roster: []SnapRole{{Name: "orc"}, {Name: "vanished"}}})
	if _, err := LoadSnapshot(cfg); err == nil || !strings.Contains(err.Error(), "vanished") {
		t.Fatalf("missing role gate: %v", err)
	}
	write(Snapshot{Version: snapshotVersion, ConfigPath: "team.toml",
		Roster: []SnapRole{{Name: "orc"}, {Name: "vanished", Gone: true}}, TaskSeq: 3})
	snap, err := LoadSnapshot(cfg)
	if err != nil || snap.TaskSeq != 3 {
		t.Fatalf("gone roles must not block resume: %v %+v", err, snap)
	}
}

func TestSpawnResumeOrderAndTombstones(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Config{Roles: []config.Role{
		{Name: "orc", Start: true, Command: "sh", Args: []string{"-c", "printf orc"}},
		{Name: "fresh", Command: "sh", Args: []string{"-c", "printf fresh"}},
	}}
	snap := &Snapshot{Version: snapshotVersion,
		Roster: []SnapRole{{Name: "retired", Gone: true}, {Name: "orc"}}}
	entries, err := spawnResume(cfg, snap, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, e := range entries {
			_ = e.pane.Close()
		}
	}()
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if !entries[0].gone || !entries[0].exited || entries[0].role.Name != "retired" {
		t.Fatalf("index 0 must stay the tombstone: %+v", entries[0])
	}
	if err := entries[0].pane.Input([]byte("x")); !errors.Is(err, pane.ErrPaneClosed) {
		t.Fatalf("tombstone pane must refuse input, got %v", err)
	}
	if entries[1].role.Name != "orc" || !entries[1].role.Start {
		t.Fatalf("index 1 must be the start role from config: %+v", entries[1].role)
	}
	if entries[2].role.Name != "fresh" || entries[2].gone {
		t.Fatalf("config-only roles append after the snapshot roster: %+v", entries[2].role)
	}
}

func TestSpawnResumeFailureClosesSpawned(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Config{Roles: []config.Role{
		{Name: "orc", Start: true, Command: "sh", Args: []string{"-c", "printf orc"}},
		{Name: "broken", Command: "definitely-not-a-command-xyz"},
	}}
	snap := &Snapshot{Version: snapshotVersion, Roster: []SnapRole{{Name: "orc"}, {Name: "broken"}}}
	if _, err := spawnResume(cfg, snap, 40, 6, "", ""); err == nil {
		t.Fatal("a failing spawn must fail the resume")
	}
}
