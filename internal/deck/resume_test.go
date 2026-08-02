// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
	"github.com/sphragis-oss/choragos/internal/wire"
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
	_ = entries[0].pane.Resize(20, 5) // resizes are a no-op on a tombstone
	if entries[1].role.Name != "orc" || !entries[1].role.Start {
		t.Fatalf("index 1 must be the start role from config: %+v", entries[1].role)
	}
	if entries[2].role.Name != "fresh" || entries[2].gone {
		t.Fatalf("config-only roles append after the snapshot roster: %+v", entries[2].role)
	}
}

func TestSaveSnapshotFailuresAreBestEffort(t *testing.T) {
	t.Chdir(t.TempDir())
	s := &session{}
	if err := os.WriteFile(contextDir, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot() // write fails: contextDir is a file; must only log
	if err := os.Remove(contextDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(contextDir, snapshotName), 0o755); err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot() // rename fails: session.json is a directory; must only log
	if _, err := LoadSnapshot(config.Config{}); err == nil {
		t.Fatal("no snapshot should have landed")
	}
}

func TestStartAllResumeRestoresStateAndLayout(t *testing.T) {
	t.Chdir(t.TempDir())
	short, err := os.MkdirTemp("/tmp", "cg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	t.Setenv("XDG_RUNTIME_DIR", short)
	off := false
	cfg := config.Config{Roles: []config.Role{
		{Name: "orchestrator", Start: true, Command: "sh", Args: []string{"-c", "exec cat"}},
		{Name: "reviewer", Command: "sh", Args: []string{"-c", "exec cat"}},
	}}
	cfg.Sphragis.Enabled = &off
	layout := wm.New(2).Marshal() // focused on index 2, not the start role
	snap := &Snapshot{Version: snapshotVersion, TaskSeq: 4,
		Roster: []SnapRole{{Name: "old", Gone: true}, {Name: "orchestrator"}, {Name: "reviewer"}},
		Board:  []wire.Task{{At: time.Now().UnixNano(), Kind: "delegate", ID: "T4", To: "reviewer", Task: "restored"}},
		Layout: layout}
	m := &Model{session: &session{cfg: cfg, resume: snap}, w: 160, h: 48}
	m.wireSession()
	if _, err := m.startAll(); err != nil {
		t.Fatal(err)
	}
	defer m.closeAll()
	if len(m.panes) != 3 || !m.panes[0].gone || m.panes[1].role.Name != "orchestrator" {
		t.Fatalf("roster not restored: %+v", m.panes)
	}
	if m.taskSeq != 4 || len(m.board) != 1 || m.board[0].id != "T4" {
		t.Fatalf("board not restored: seq=%d board=%+v", m.taskSeq, m.board)
	}
	if m.active != 2 || m.tree.FocusedRole() != 2 {
		t.Fatalf("layout not restored: active=%d focused=%d", m.active, m.tree.FocusedRole())
	}
}

func TestQuitAndPrefixActionsCaptureLayout(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := &entry{role: config.Role{Name: "solo", Start: true},
		pane: pane.Remote(80, 24, func([]byte) error { return nil }, func(int, int) {})}
	m := newTestModel([]*entry{e})
	m.prefixed = true
	m.handleKey(key(m.keys.Zoom))
	if len(m.layout) == 0 {
		t.Fatal("a prefix wm action must capture the layout")
	}
	// attached clients also checkpoint the layout to the server
	c1, c2 := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, c2) }()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	m.remote = wire.NewConn(c1)
	m.prefixed = true
	m.handleKey(key(m.keys.Zoom))
	m.remote = nil
	m.layout = nil
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if cmd == nil {
		t.Fatal("ctrl+q must quit")
	}
	if len(m.layout) == 0 {
		t.Fatal("ctrl+q must capture the layout for the snapshot")
	}
	if _, err := os.Stat(filepath.Join(contextDir, snapshotName)); err != nil {
		t.Fatalf("quit must write the session snapshot: %v", err)
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
	// same for a config-added role the snapshot never knew
	snap = &Snapshot{Version: snapshotVersion, Roster: []SnapRole{{Name: "orc"}}}
	if _, err := spawnResume(cfg, snap, 40, 6, "", ""); err == nil {
		t.Fatal("a failing appended spawn must fail the resume")
	}
}
