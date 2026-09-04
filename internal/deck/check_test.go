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
)

func TestRunCheck(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "out.log")

	m := runCheck("T1", "echo hi", dir, file, time.Minute)
	if m.fail != "" || m.exit != 0 {
		t.Fatalf("pass: %+v", m)
	}
	body, err := os.ReadFile(file)
	if err != nil || !strings.HasPrefix(string(body), "# check: echo hi\n# dir: "+dir+"\n# exit 0") || !strings.HasSuffix(string(body), "\n\nhi\n") {
		t.Fatalf("report: err=%v body=%q", err, body)
	}

	m = runCheck("T1", "echo boom; exit 3", dir, file, time.Minute)
	if m.fail != "" || m.exit != 3 {
		t.Fatalf("fail: %+v", m)
	}
	if body, _ := os.ReadFile(file); !strings.Contains(string(body), "# exit 3") || !strings.Contains(string(body), "boom") {
		t.Fatalf("fail report: %q", body)
	}

	m = runCheck("T1", "sleep 5", dir, file, 50*time.Millisecond)
	if !strings.Contains(m.fail, "timed out after 50ms") {
		t.Fatalf("timeout: %+v", m)
	}
	if body, _ := os.ReadFile(file); !strings.Contains(string(body), "# check timed out") {
		t.Fatalf("timeout report: %q", body)
	}

	m = runCheck("T1", "true", filepath.Join(dir, "missing"), file, time.Minute)
	if !strings.HasPrefix(m.fail, "check unavailable:") {
		t.Fatalf("missing dir: %+v", m)
	}

	m = runCheck("T1", "true", dir, filepath.Join(dir, "missing", "out.log"), time.Minute)
	if !strings.HasPrefix(m.fail, "check unavailable:") {
		t.Fatalf("unwritable report: %+v", m)
	}

	m = runCheck("T1", "head -c 100000 /dev/zero | tr '\\0' x; echo; echo TAIL", dir, file, time.Minute)
	body, _ = os.ReadFile(file)
	if m.exit != 0 || len(body) > checkOutputCap+256 || !strings.HasSuffix(string(body), "TAIL\n") {
		t.Fatalf("tail cap: exit=%d len=%d", m.exit, len(body))
	}
}

// checkPump wires the notify pump to a channel; it must be installed before any check goroutine starts.
func checkPump(m *Model) chan any {
	ch := make(chan any, 16)
	m.notify = func(v any) { ch <- v }
	return ch
}

// pumpCheck returns the next checkMsg from the pump, applied to the model.
func pumpCheck(t *testing.T, m *Model, ch chan any) checkMsg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case v := <-ch:
			if msg, ok := v.(checkMsg); ok {
				m.finishCheck(msg)
				return msg
			}
		case <-deadline:
			t.Fatal("no check result arrived")
		}
	}
}

func TestCheckPassWithoutJudgeAccepts(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Check: "true"})
	m := newTestModel(panes)
	ch := checkPump(m)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "CHECKED-1 build"})
	if m.loops["T1"] == nil {
		t.Fatalf("loop not registered for a check-only role: %v", m.loops)
	}
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "built", Report: "/tmp/r1.md"})
	if l := m.loops["T1"]; l == nil || l.phase != "check" {
		t.Fatalf("loop not in check phase: %+v", m.loops)
	}
	msg := pumpCheck(t, m, ch)
	if msg.exit != 0 || msg.fail != "" {
		t.Fatalf("check result: %+v", msg)
	}
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "passed the check for task T1") }) {
		t.Fatalf("pass not reported to orchestrator:\n%q", panes[0].pane.Render())
	}
	if len(m.loops) != 0 || len(m.gates) != 0 {
		t.Fatalf("loop or gate left behind: loops=%v gates=%+v", m.loops, m.gates)
	}
	for _, ev := range m.board {
		if ev.kind == "delegate" && ev.id == "T1" && ev.score != "check ok" {
			t.Errorf("board row not annotated: %+v", ev)
		}
	}
	if _, err := os.Stat(filepath.Join(contextDir, "check-T1-r1.log")); err != nil {
		t.Errorf("check report missing: %v", err)
	}
}

func TestCheckFailRetriesThenCapFailsClosed(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Check: "echo nope >&2; exit 2", JudgeRounds: 2})
	m := newTestModel(panes)
	ch := checkPump(m)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "CHECKED-2 harder"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "attempt 1"})
	pumpCheck(t, m, ch)
	loop := m.loops["T2"]
	if loop == nil || loop.round != 2 || loop.phase != "build" {
		t.Fatalf("retry round not delivered: %+v", m.loops)
	}
	body, err := os.ReadFile(filepath.Join(contextDir, "worker-task-coder.md"))
	if err != nil || !strings.Contains(string(body), "failed the check command (exit 2)") || !strings.Contains(string(body), "check-T1-r1.log") || !strings.Contains(string(body), "CHECKED-2 harder") {
		t.Fatalf("retry task lacks critique or original task: err=%v body=%q", err, body)
	}
	if out, _ := os.ReadFile(filepath.Join(contextDir, "check-T1-r1.log")); !strings.Contains(string(out), "nope") {
		t.Fatalf("stderr not captured: %q", out)
	}

	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T2", Task: "attempt 2"})
	pumpCheck(t, m, ch)
	if len(m.gates) != 1 || !strings.Contains(m.gates[0].reason, "check cap exhausted") || !strings.HasSuffix(m.gates[0].report, "check-T1-r2.log") {
		t.Fatalf("cap exhaustion did not gate: %+v", m.gates)
	}
	if len(m.loops) != 0 {
		t.Errorf("loop leaked after cap: %v", m.loops)
	}
}

func TestCheckPassThenJudge(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Check: "true", Judge: "reviewer", JudgePass: 5})
	m := newTestModel(panes)
	ch := checkPump(m)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "CHECKED-3"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1", Task: "built"})
	if strings.Contains(panes[2].pane.Render(), "judge-task-reviewer.md") {
		t.Fatal("judge consulted before the check finished")
	}
	pumpCheck(t, m, ch)
	if !waitFor(func() bool { return strings.Contains(panes[2].pane.Render(), "judge-task-reviewer.md") }) {
		t.Fatalf("judge round not injected after the check passed:\n%q", panes[2].pane.Render())
	}
	if l := m.loops["T2"]; l == nil || l.phase != "judge" {
		t.Fatalf("loop not in judge phase: %+v", m.loops)
	}
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T2", Task: "judged", Report: verdictFile(t, "9/10")})
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "passed judge review") }) {
		t.Fatalf("pass not reported:\n%q", panes[0].pane.Render())
	}
}

func TestCheckTimeoutFailsClosed(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Check: "sleep 5", CheckTimeout: "50ms"})
	m := newTestModel(panes)
	ch := checkPump(m)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "CHECKED-4"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1"})
	pumpCheck(t, m, ch)
	if len(m.gates) != 1 || !strings.Contains(m.gates[0].reason, "check timed out") {
		t.Fatalf("timeout did not gate: %+v", m.gates)
	}
	if len(m.loops) != 0 {
		t.Errorf("loop leaked: %v", m.loops)
	}
}

func TestCheckBuilderGoneFailsClosed(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Check: "true"})
	m := newTestModel(panes)
	ch := checkPump(m)

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "CHECKED-5"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1"})
	panes[1].gone = true
	pumpCheck(t, m, ch)
	if len(m.gates) != 1 || !strings.Contains(m.gates[0].reason, "builder unavailable for retry") && !strings.Contains(m.gates[0].reason, "builder role is gone") {
		t.Fatalf("gone builder did not gate: %+v", m.gates)
	}
}

func TestCheckWorktreeDirMissingFailsClosed(t *testing.T) {
	panes := startJudgePanes(t, config.Role{Name: "coder", Command: "cat", Check: "true"})
	m := newTestModel(panes)
	ch := checkPump(m)
	panes[1].role.Worktree = true // no checkout exists: the check cannot run

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "CHECKED-6"})
	m.dispatch(ipc.Command{Cmd: "work-done", ID: "T1"})
	pumpCheck(t, m, ch)
	if len(m.gates) != 1 || !strings.Contains(m.gates[0].reason, "check unavailable") {
		t.Fatalf("missing worktree did not gate: %+v", m.gates)
	}
}

func TestCheckResultForUnknownLoopIsDropped(t *testing.T) {
	m := newTestModel(nil)
	m.finishCheck(checkMsg{id: "T9", file: "x.log"})
	m.Update(checkMsg{id: "T9"})
	(&server{sess: m.session}).handle(checkMsg{id: "T9"})
	if len(m.gates) != 0 || len(m.loops) != 0 {
		t.Fatalf("stray result changed state: gates=%+v loops=%v", m.gates, m.loops)
	}
}
