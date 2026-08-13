// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
)

func remoteEntry(name string, start bool) *entry {
	return &entry{role: config.Role{Name: name, Start: start},
		pane: pane.Remote(80, 24, func([]byte) error { return nil }, func(int, int) {})}
}

func TestHandoffWaitsForDocumentThenStamps(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &session{cfg: config.Config{Path: "old.toml"}, panes: []*entry{remoteEntry("orc", true)}}
	if s.checkHandoff() {
		t.Fatal("no handoff pending: check must be a no-op")
	}
	s.startHandoff("/abs/new-team.toml")
	s.startHandoff("ignored.toml") // second request is a no-op
	if s.handoffCfg != "/abs/new-team.toml" {
		t.Fatalf("handoffCfg = %q", s.handoffCfg)
	}
	if s.checkHandoff() {
		t.Fatal("no document yet: the deck must keep waiting")
	}
	s.handoffAt = time.Now() // reset after the fs mtime writes below
	if err := os.WriteFile(filepath.Join(contextDir, handoffFile), []byte("# handoff"), 0o644); err != nil {
		t.Fatal(err)
	}
	// mtime granularity: stamp the request before the write
	s.handoffAt = time.Now().Add(-time.Second)
	if !s.checkHandoff() {
		t.Fatal("document written: the deck must stop")
	}
	if !s.handoffDone || s.cfg.Path != "/abs/new-team.toml" || s.layout != nil {
		t.Fatalf("finishHandoff: done=%v path=%q layout=%v", s.handoffDone, s.cfg.Path, s.layout)
	}
	s.saveSnapshot()
	snap, err := LoadSnapshot(config.Config{Path: "/abs/new-team.toml", Roles: []config.Role{{Name: "orc", Start: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Handoff {
		t.Fatal("quit snapshot must carry the handoff flag")
	}
}

func TestHandoffAsksForMemoryEntryFirst(t *testing.T) {
	t.Chdir(t.TempDir())
	var got []byte
	e := &entry{role: config.Role{Name: "orc", Start: true},
		pane: pane.Remote(80, 24, func(b []byte) error { got = append(got, b...); return nil }, func(int, int) {})}
	s := &session{cfg: config.Config{Path: "team.toml"}, panes: []*entry{e}}
	s.startHandoff("")
	mi := strings.Index(string(got), memoryFile)
	hi := strings.Index(string(got), handoffFile)
	if mi < 0 || hi < 0 || mi > hi {
		t.Fatalf("request must ask for the memory entry before the handoff document: %q", got)
	}
}

func TestHandoffTimesOutWithoutDocument(t *testing.T) {
	t.Chdir(t.TempDir())
	s := &session{cfg: config.Config{Path: "team.toml"}, panes: []*entry{remoteEntry("orc", true)}}
	s.startHandoff("")
	s.handoffAt = time.Now().Add(-handoffTimeout - time.Second)
	if !s.checkHandoff() {
		t.Fatal("past the timeout the deck must stop anyway")
	}
	if !s.handoffDone || s.cfg.Path != "team.toml" {
		t.Fatalf("finishHandoff without --config: done=%v path=%q", s.handoffDone, s.cfg.Path)
	}
}

func TestLoadSnapshotHandoffTombstonesDroppedRoles(t *testing.T) {
	t.Chdir(t.TempDir())
	s := &session{cfg: config.Config{Path: "new.toml"}, handoffDone: true, panes: []*entry{
		remoteEntry("orc", true), remoteEntry("translator", false),
	}}
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot()
	cfg := config.Config{Path: "new.toml", Roles: []config.Role{{Name: "orc", Start: true}, {Name: "sec-reviewer"}}}
	snap, err := LoadSnapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Roster[1].Gone {
		t.Fatalf("dropped role must become a tombstone on a handoff resume: %+v", snap.Roster)
	}
	if snap.Roster[0].Gone {
		t.Fatal("kept roles must stay live")
	}
}

func TestHandoffNoteOnlyOnResumeWithDocument(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &session{}
	if s.handoffNote() != "" {
		t.Fatal("fresh session: no note")
	}
	s.resume = &Snapshot{Version: snapshotVersion}
	if s.handoffNote() != "" {
		t.Fatal("resumed without a document: no note")
	}
	if err := os.WriteFile(filepath.Join(contextDir, handoffFile), []byte("# handoff"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.handoffNote(), handoffFile) {
		t.Fatalf("resumed with a document: note must point at it, got %q", s.handoffNote())
	}
	// and injectBoot lands it in the orchestrator context file
	e := remoteEntry("orc", true)
	s.cfg = config.Config{Roles: []config.Role{{Name: "orc", Start: true}}}
	s.panes = []*entry{e}
	s.injectBoot(e)
	ctx, err := os.ReadFile(filepath.Join(contextDir, "orchestrator-context.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), handoffFile) {
		t.Fatalf("orchestrator context must reference the handoff document:\n%s", ctx)
	}
}

func TestHandoffOverlayAndIPC(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestModel([]*entry{remoteEntry("orc", true)})
	m.prefixed = true
	m.handleKey(key(m.keys.Handoff))
	if !m.hoOn {
		t.Fatal("prefix+H must open the confirm overlay")
	}
	if got := m.renderHandoff(80, 20); !strings.Contains(got, handoffFile) {
		t.Fatalf("overlay must explain the flow:\n%s", got)
	}
	m.handleKey(key("x"))
	if m.hoOn || !m.handoffAt.IsZero() {
		t.Fatal("any other key cancels without starting a handoff")
	}
	m.prefixed = true
	m.handleKey(key(m.keys.Handoff))
	m.handleKey(key("y"))
	if m.hoOn || m.handoffAt.IsZero() {
		t.Fatal("y must start the handoff")
	}
}

func TestServerHandoffStopsAndStampsSnapshot(t *testing.T) {
	done := startTestServer(t)
	if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "handoff"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // the document must be younger than the request
	if err := os.WriteFile(filepath.Join(contextDir, handoffFile), []byte("# handoff"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exit: %v", err)
		}
		done <- nil // the harness cleanup waits on done too
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop after the handoff document landed")
	}
	snap, err := LoadSnapshot(config.Config{Path: "team.toml", Roles: []config.Role{{Name: "orchestrator", Start: true}, {Name: "reviewer"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Handoff || len(snap.Layout) != 0 {
		t.Fatalf("quit snapshot: handoff=%v layout=%d bytes", snap.Handoff, len(snap.Layout))
	}
}

func TestHandoffIPCTickQuits(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestModel([]*entry{remoteEntry("orc", true)})
	m.Update(ipcMsg{cmd: ipc.Command{Cmd: "handoff", NextConfig: "/abs/next.toml"}})
	if m.handoffAt.IsZero() || m.handoffCfg != "/abs/next.toml" {
		t.Fatalf("ipc handoff must start the flow: at=%v cfg=%q", m.handoffAt, m.handoffCfg)
	}
	if err := os.WriteFile(filepath.Join(contextDir, handoffFile), []byte("# handoff"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.handoffAt = time.Now().Add(-time.Second)
	_, cmd := m.Update(tickMsg{})
	if cmd == nil {
		t.Fatal("tick after the document lands must quit")
	}
	if _, err := os.Stat(filepath.Join(contextDir, snapshotName)); err != nil {
		t.Fatalf("quit must leave the snapshot: %v", err)
	}
}
