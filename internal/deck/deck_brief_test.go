// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
)

func TestDispatchDelegateWithBrief(t *testing.T) {
	brief := filepath.Join(t.TempDir(), "task-brief.md")
	if err := os.WriteFile(brief, []byte("# Mission\n\nbuild the thing"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // isolate the generated .choragos/ context files
	panes, err := startPanes(config.Config{Roles: []config.Role{
		{Name: "orchestrator", Command: "cat", Start: true},
		{Name: "coder", Command: "cat"},
	}}, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, e := range panes {
			_ = e.pane.Close()
		}
	}()
	for _, e := range panes {
		go func(p *pane.Pane) { _ = p.Stream(nil) }(e.pane)
	}
	m := newTestModel(panes)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Brief: brief})
	if !waitFor(func() bool { return strings.Contains(panes[1].pane.Render(), "worker-task-coder.md") }) {
		t.Fatalf("delegate one-liner not injected into coder:\n%q", panes[1].pane.Render())
	}
	body, err := os.ReadFile(filepath.Join(".choragos", "worker-task-coder.md"))
	if err != nil || !strings.Contains(string(body), "Read "+brief+" for the full brief.") {
		t.Fatalf("task file missing brief pointer: err=%v body=%q", err, string(body))
	}
	ev := m.board[len(m.board)-1]
	if ev.file != brief || ev.task != "brief: task-brief.md" {
		t.Fatalf("board event = %+v, want brief path and derived label", ev)
	}

	report := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(report, []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.dispatch(ipc.Command{Cmd: "work-done", ID: ev.id, Report: report})
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "A worker reports") }) {
		t.Fatalf("work-done not injected into orchestrator:\n%q", panes[0].pane.Render())
	}
	ev = m.board[len(m.board)-1]
	if ev.kind != "work-done" || ev.file != report || ev.task != "see report" {
		t.Fatalf("work-done board event = %+v", ev)
	}
}
