// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"os"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
)

// startOwnModel boots a cat team where qa owns defects.md; dev tweaks the second role.
func startOwnModel(t *testing.T, dev config.Role) (*Model, []*entry) {
	t.Helper()
	t.Chdir(t.TempDir())
	cfg := config.Config{Roles: []config.Role{
		{Name: "orchestrator", Command: "cat", Start: true},
		dev,
		{Name: "qa", Command: "cat", OwnsFiles: []string{"defects.md"}},
		{Name: "reviewer", Command: "cat"},
	}}
	panes, err := startPanes(cfg, 60, 8, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, e := range panes {
			_ = e.pane.Close()
		}
	})
	for _, e := range panes {
		go func(p *pane.Pane) { _ = p.Stream(nil) }(e.pane)
	}
	m := newTestModel(panes)
	m.session.cfg = cfg
	return m, panes
}

func writeDefects(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile("defects.md", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOwnershipCleanWorkDone(t *testing.T) {
	m, panes := startOwnModel(t, config.Role{Name: "dev", Command: "cat"})
	writeDefects(t, "open: bug 1\n")

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"dev"}, Task: "fix bug 1"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "fixed"})
	if len(m.gates) != 0 {
		t.Fatalf("clean work-done gated: %+v", m.gates)
	}
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "A worker reports") }) {
		t.Fatalf("orchestrator not told:\n%q", panes[0].pane.Render())
	}
	if len(m.ownSnaps) != 0 {
		t.Errorf("snapshot leaked: %v", m.ownSnaps)
	}
}

func TestOwnershipOwnerMayWrite(t *testing.T) {
	m, _ := startOwnModel(t, config.Role{Name: "dev", Command: "cat"})
	writeDefects(t, "open: bug 1\n")

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"qa"}, Task: "close bug 1"})
	writeDefects(t, "closed: bug 1\n")
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "closed"})
	if len(m.gates) != 0 {
		t.Fatalf("owner's own write gated: %+v", m.gates)
	}
}

func TestOwnershipViolationGatesAndResolves(t *testing.T) {
	m, panes := startOwnModel(t, config.Role{Name: "dev", Command: "cat"})
	writeDefects(t, "open: bug 1\n")

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"dev"}, Task: "fix bug 1"})
	writeDefects(t, "closed: bug 1\n") // dev closes its own bug
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "fixed and closed"})

	if len(m.gates) != 1 || !m.gates[0].ownership {
		t.Fatalf("violation did not gate: %+v", m.gates)
	}
	if !strings.Contains(m.gates[0].reason, "defects.md (owned by qa)") {
		t.Fatalf("reason lacks file and owner: %q", m.gates[0].reason)
	}
	if strings.Contains(panes[0].pane.Render(), "A worker reports") {
		t.Fatal("orchestrator heard a gated work-done")
	}

	m.approveGate()
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "accepted dev's work") }) {
		t.Fatalf("accept not forwarded:\n%q", panes[0].pane.Render())
	}
}

func TestOwnershipViolationRejected(t *testing.T) {
	m, panes := startOwnModel(t, config.Role{Name: "dev", Command: "cat"})

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"dev"}, Task: "fix bug"})
	writeDefects(t, "closed\n") // creation counts as a write
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "done"})
	if len(m.gates) != 1 {
		t.Fatalf("creation did not gate: %+v", m.gates)
	}
	m.rejectGate()
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "rejected dev's work") }) {
		t.Fatalf("reject not forwarded:\n%q", panes[0].pane.Render())
	}
}

func TestOwnershipAmbiguousOverlapWarnsOnly(t *testing.T) {
	m, panes := startOwnModel(t, config.Role{Name: "dev", Command: "cat"})
	writeDefects(t, "open: bug 1\n")

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"qa"}, Task: "triage"})
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"dev"}, Task: "fix bug 1"})
	writeDefects(t, "closed: bug 1\n") // could be either; qa is still in flight
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T2", Task: "fixed"})

	if len(m.gates) != 0 {
		t.Fatalf("ambiguous change gated: %+v", m.gates)
	}
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "attribution is ambiguous") }) {
		t.Fatalf("ambiguity warning missing:\n%q", panes[0].pane.Render())
	}
}

func TestOwnershipUnreadableFailsClosed(t *testing.T) {
	m, _ := startOwnModel(t, config.Role{Name: "dev", Command: "cat"})
	if err := os.Mkdir("defects.md", 0o700); err != nil { // unreadable as a file, before and after
		t.Fatal(err)
	}
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"dev"}, Task: "fix"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "done"})
	if len(m.gates) != 1 {
		t.Fatalf("unreadable owned file did not fail toward the gate: %+v", m.gates)
	}
}

func TestOwnershipJudgeLoopViolationFailsClosed(t *testing.T) {
	m, panes := startOwnModel(t, config.Role{Name: "dev", Command: "cat", Judge: "reviewer"})
	writeDefects(t, "open: bug 1\n")

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"dev"}, Task: "fix bug 1"})
	if m.loops["T1"] == nil {
		t.Fatalf("judge loop not registered: %v", m.loops)
	}
	writeDefects(t, "closed: bug 1\n")
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "fixed"})

	if len(m.gates) != 1 || !strings.Contains(m.gates[0].reason, "ownership violation") {
		t.Fatalf("judged violation did not fail closed: %+v", m.gates)
	}
	if len(m.loops) != 0 {
		t.Errorf("loop survived a violation: %v", m.loops)
	}
	if strings.Contains(panes[3].pane.Render(), "judge-task") {
		t.Error("judge round delivered despite violation")
	}
}
