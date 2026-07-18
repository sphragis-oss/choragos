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

func TestParseVerdict(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	valid := map[string]int{
		"VERDICT: 8/10\n\nSolid work.":    8,
		"\n\n  VERDICT: 0/10  \ncritique": 0,
		"VERDICT: 10/10":                  10,
	}
	for body, want := range valid {
		got, err := parseVerdict(write("v.md", body))
		if err != nil || got != want {
			t.Errorf("parseVerdict(%q) = %d, %v; want %d", body, got, err, want)
		}
	}
	invalid := []string{
		"The work looks good.\nVERDICT: 8/10", // prose first
		"VERDICT: pass",
		"VERDICT: 11/10",
		"VERDICT: -1/10",
		"VERDICT: 8/9",
		"VERDICT: eight/10",
		"",
	}
	for _, body := range invalid {
		if got, err := parseVerdict(write("v.md", body)); err == nil {
			t.Errorf("parseVerdict(%q) = %d, want error", body, got)
		}
	}
	if _, err := parseVerdict(""); err == nil {
		t.Error("empty path accepted")
	}
	if _, err := parseVerdict(filepath.Join(dir, "missing.md")); err == nil {
		t.Error("missing file accepted")
	}
}

// startJudgePanes boots a cat team wired for judging: coder is scored by reviewer.
func startJudgePanes(t *testing.T, coder config.Role) []*entry {
	t.Helper()
	t.Chdir(t.TempDir())
	cfg := config.Config{Roles: []config.Role{
		{Name: "orchestrator", Command: "cat", Start: true},
		coder,
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
	return panes
}

func verdictFile(t *testing.T, score string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "verdict.md")
	if err := os.WriteFile(p, []byte("VERDICT: "+score+"\n\nCritique body.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJudgeLoopPass(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Judge: "reviewer", JudgePass: 5})
	m := newTestModel(panes)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "JUDGED-1 build the thing"})
	if len(m.loops) != 1 || m.loops["T1"] == nil {
		t.Fatalf("loop not registered for T1: %v", m.loops)
	}
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "built"})
	if !waitFor(func() bool { return strings.Contains(panes[2].pane.Render(), "judge-task-reviewer.md") }) {
		t.Fatalf("judge round not injected into reviewer:\n%q", panes[2].pane.Render())
	}
	if m.loops["T2"] == nil || m.loops["T2"].phase != "judge" {
		t.Fatalf("loop not advanced to judge phase: %+v", m.loops)
	}
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T2", Task: "judged", Report: verdictFile(t, "7/10")})
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "passed judge review") }) {
		t.Fatalf("pass not reported to orchestrator:\n%q", panes[0].pane.Render())
	}
	if len(m.loops) != 0 {
		t.Errorf("loop not cleared after pass: %v", m.loops)
	}
	if len(m.gates) != 0 {
		t.Errorf("unexpected gate after pass: %+v", m.gates)
	}
	var judged *taskEvent
	for i := range m.board {
		if m.board[i].id == "T2" && m.board[i].kind == "delegate" {
			judged = &m.board[i]
		}
	}
	if judged == nil || judged.score != "7/10" || judged.round != 1 {
		t.Errorf("judge round row missing score: %+v", judged)
	}
}

func TestJudgeLoopRetryThenCapFailsClosed(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Judge: "reviewer", JudgePass: 8, JudgeRounds: 2})
	m := newTestModel(panes)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "JUDGED-2 harder thing"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "attempt 1"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T2", Task: "judged", Report: verdictFile(t, "3/10")})
	loop := m.loops["T3"]
	if loop == nil || loop.round != 2 || loop.phase != "build" {
		t.Fatalf("retry round not delivered: %+v", m.loops)
	}
	if !waitFor(func() bool { return strings.Contains(panes[1].pane.Render(), "worker-task-coder.md") }) {
		t.Fatalf("retry not injected into coder:\n%q", panes[1].pane.Render())
	}
	body, err := os.ReadFile(filepath.Join(".choragos", "worker-task-coder.md"))
	if err != nil || !strings.Contains(string(body), "scored 3/10") || !strings.Contains(string(body), "JUDGED-2 harder thing") {
		t.Fatalf("retry task lacks critique or original task: err=%v body=%q", err, body)
	}

	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T3", Task: "attempt 2"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T4", Task: "judged", Report: verdictFile(t, "4/10")})
	if len(m.gates) != 1 || !strings.Contains(m.gates[0].reason, "cap exhausted") {
		t.Fatalf("cap exhaustion did not gate: %+v", m.gates)
	}
	if len(m.loops) != 0 {
		t.Errorf("loop leaked after cap: %v", m.loops)
	}

	m.approveGate()
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "accepted coder's work") }) {
		t.Fatalf("accept not reported to orchestrator:\n%q", panes[0].pane.Render())
	}
}

func TestJudgeInvalidVerdictFailsClosed(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Judge: "reviewer"})
	m := newTestModel(panes)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "JUDGED-3"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1"})
	bad := filepath.Join(t.TempDir(), "prose.md")
	if err := os.WriteFile(bad, []byte("Looks great to me!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T2", Task: "judged", Report: bad})
	if len(m.gates) != 1 || !strings.Contains(m.gates[0].reason, "unparseable verdict") {
		t.Fatalf("invalid verdict did not gate: %+v", m.gates)
	}

	m.rejectGate()
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "rejected the result") }) {
		t.Fatalf("reject not reported to orchestrator:\n%q", panes[0].pane.Render())
	}
	if len(m.gates) != 0 {
		t.Errorf("gate not cleared: %+v", m.gates)
	}
}

func TestJudgeMissingReportFailsClosed(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Judge: "reviewer"})
	m := newTestModel(panes)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "JUDGED-4"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T2", Task: "judged"}) // no --report
	if len(m.gates) != 1 || !strings.Contains(m.gates[0].reason, "unparseable verdict") {
		t.Fatalf("missing report did not gate: %+v", m.gates)
	}
}

func TestJudgeWireRoundTrip(t *testing.T) {
	board := []taskEvent{{kind: "delegate", id: "T2", to: "reviewer", round: 2, score: "6/10"}}
	gates := []pendingGate{{to: "coder", reason: "judge cap exhausted", report: "/tmp/r.md", loopID: "T1"}}
	tasks := fromWireTasks(toWireTasks(board))
	if tasks[0].round != 2 || tasks[0].score != "6/10" {
		t.Errorf("task round/score lost on the wire: %+v", tasks[0])
	}
	back := fromWireGates(toWireGates(gates))
	if back[0].reason != "judge cap exhausted" || back[0].report != "/tmp/r.md" {
		t.Errorf("gate reason/report lost on the wire: %+v", back[0])
	}
}
