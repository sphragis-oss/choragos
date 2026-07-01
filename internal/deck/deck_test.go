// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
)

func TestStartPanesSpawnsAllRoles(t *testing.T) {
	cfg := config.Config{Roles: []config.Role{
		{Name: "a", Command: "sh", Args: []string{"-c", "printf role-alpha"}},
		{Name: "b", Command: "sh", Args: []string{"-c", "printf role-beta"}},
		{Name: "c", Command: "sh", Args: []string{"-c", "printf role-gamma"}},
	}}
	panes, err := startPanes(cfg, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, e := range panes {
			_ = e.pane.Close()
		}
	}()
	if len(panes) != 3 {
		t.Fatalf("want 3 panes, got %d", len(panes))
	}
	for i, want := range []string{"role-alpha", "role-beta", "role-gamma"} {
		if panes[i].role.Name != string(rune('a'+i)) {
			t.Errorf("pane %d role name = %q", i, panes[i].role.Name)
		}
		waitStream(t, panes[i].pane)
		if !strings.Contains(panes[i].pane.Render(), want) {
			t.Fatalf("pane %d missing %q:\n%q", i, want, panes[i].pane.Render())
		}
	}
}

func TestRoleArgsAppendsModel(t *testing.T) {
	got := roleArgs(config.Role{Command: "claude", Args: []string{"-p"}, Model: "opus"})
	if strings.Join(got, " ") != "-p --model opus" {
		t.Fatalf("roleArgs = %v", got)
	}
	if bare := roleArgs(config.Role{Command: "sh"}); len(bare) != 0 {
		t.Fatalf("want no args without model, got %v", bare)
	}
}

func TestComputeLayout(t *testing.T) {
	// 120x40, 5 roles: left ~1/3, right the rest, columns fill width.
	d := computeLayout(5, 120, 40)
	if d.leftW+d.rightW != 120 {
		t.Errorf("leftW(%d)+rightW(%d) = %d, want 120", d.leftW, d.rightW, d.leftW+d.rightW)
	}
	if d.leftW != 40 {
		t.Errorf("leftW = %d, want 40", d.leftW)
	}
	if d.paneW != d.rightW-2 {
		t.Errorf("paneW = %d, want rightW-2 = %d", d.paneW, d.rightW-2)
	}
	if d.paneH != 40-5-3 {
		t.Errorf("paneH = %d, want %d", d.paneH, 40-5-3)
	}
	// tiny terminal: widths stay valid; paneH may be <= 0 (box is skipped).
	n := computeLayout(5, 30, 5)
	if n.leftW+n.rightW != 30 || n.leftW < 1 || n.rightW < 1 || n.paneW < 1 {
		t.Errorf("bad widths on tiny terminal: %+v", n)
	}
	if n.paneH > 0 {
		t.Errorf("expected non-positive paneH on tiny terminal, got %d", n.paneH)
	}
}

func TestComputeStatus(t *testing.T) {
	now := time.Now()
	if s := computeStatus(&entry{lastActive: now}, now); s.dot != "●" || s.label != "working" || !s.working {
		t.Errorf("recent = %q/%q, want working", s.dot, s.label)
	}
	if s := computeStatus(&entry{lastActive: now.Add(-10 * time.Second)}, now); s.label != "idle 10s ago" {
		t.Errorf("stale label = %q", s.label)
	}
	if s := computeStatus(&entry{exited: true}, now); s.dot != "○" || s.label != "exited" || !s.exited {
		t.Errorf("exited = %q/%q", s.dot, s.label)
	}
}

func TestPromptInLines(t *testing.T) {
	blocking := [][]string{
		{"Allow access to this file?", "1. Yes, allow access"},
		{"  esc to cancel"},
		{"Do you want to proceed? (y/n)"},
		{"Overwrite? [y/N]"},
	}
	for _, lines := range blocking {
		if !promptInLines(lines) {
			t.Errorf("expected blocking prompt in %q", lines)
		}
	}
	if promptInLines([]string{"Thought for 5s", "Read 1 file", "Standing by."}) {
		t.Error("false positive on ordinary output")
	}
}

func TestTruncate(t *testing.T) {
	cases := map[string]struct {
		max  int
		want string
	}{
		"short":     {10, "short"},
		"toolongxx": {5, "tool…"},
	}
	for in, c := range cases {
		if got := truncate(in, c.max); got != c.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", in, c.max, got, c.want)
		}
	}
	if got := truncate("anything", 0); got != "" {
		t.Errorf("max 0 = %q, want empty", got)
	}
}

func TestActivityTail(t *testing.T) {
	lines := []string{
		"My role is orchestrator.",
		"Read 1 file",
		"Opus 4.8 choragos | lazy:full",
		"[████████░░░░░░░░] 3% $0.32 10s",
		"← for agents",
		"Ready for your instructions.",
	}
	got := activityTail(lines, 3)
	want := []string{"My role is orchestrator.", "Read 1 file", "Ready for your instructions."}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if chromeLine("● Delegating the task to coder.") {
		t.Error("a bulleted content line must not be treated as chrome")
	}
}

func TestHumanizeSince(t *testing.T) {
	cases := map[time.Duration]string{
		5 * time.Second:  "5s ago",
		90 * time.Second: "1m ago",
		2 * time.Hour:    "2h ago",
	}
	for d, want := range cases {
		if got := humanizeSince(d); got != want {
			t.Errorf("humanizeSince(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestDispatchDelegateInjects(t *testing.T) {
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
	m := &Model{panes: panes}

	// delegate injects a one-liner pointing at the task file; the full task
	// (which may be multi-line) lands in the file.
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "BUILD-42 add api/login.go"})
	if !waitFor(func() bool { return strings.Contains(panes[1].pane.Render(), "worker-task-coder.md") }) {
		t.Fatalf("delegate one-liner not injected into coder:\n%q", panes[1].pane.Render())
	}
	body, err := os.ReadFile(filepath.Join(".choragos", "worker-task-coder.md"))
	if err != nil || !strings.Contains(string(body), "BUILD-42 add api/login.go") {
		t.Fatalf("task file missing content: err=%v body=%q", err, string(body))
	}
	if m.active != 1 {
		t.Errorf("active = %d, want coder (1)", m.active)
	}

	m.dispatch(ipc.Command{Cmd: "work-done", Task: "DONE-42"})
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "DONE-42") }) {
		t.Fatalf("work-done not injected into orchestrator:\n%q", panes[0].pane.Render())
	}
	if m.active != 0 {
		t.Errorf("active = %d, want orchestrator (0)", m.active)
	}
}

func TestDispatchLogsEvents(t *testing.T) {
	t.Chdir(t.TempDir())
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

	var buf bytes.Buffer
	m := &Model{panes: panes, events: slog.New(slog.NewTextHandler(&buf, nil))}
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "do X"})
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"ghost"}})
	m.dispatch(ipc.Command{Cmd: "work-done", Task: "done X"})

	out := buf.String()
	for _, want := range []string{"msg=delegate", "to=coder", "delegate target unavailable", "msg=work-done"} {
		if !strings.Contains(out, want) {
			t.Errorf("event log missing %q; got:\n%s", want, out)
		}
	}
}

func TestDispatchFailClosedRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	panes, err := startPanes(config.Config{Roles: []config.Role{
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
	go func() { _ = panes[0].pane.Stream(nil) }()

	// sphragis enforcement on, gateway down: zero-value Sphragis is fail-closed.
	m := &Model{panes: panes, sphragisOn: true, gatewayUp: false}
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "blocked task"})
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(panes[0].pane.Render(), "worker-task") {
		t.Fatalf("fail-closed did not refuse delegate:\n%q", panes[0].pane.Render())
	}

	// gateway healthy: delegation goes through.
	m.gatewayUp = true
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "allowed task"})
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "worker-task-coder.md") }) {
		t.Fatalf("delegate blocked even when gateway healthy:\n%q", panes[0].pane.Render())
	}
}

func TestBootInjectsPrompts(t *testing.T) {
	t.Chdir(t.TempDir())
	panes, err := startPanes(config.Config{Roles: []config.Role{
		{Name: "orchestrator", Command: "cat", Start: true, Prompt: "You coordinate."},
		{Name: "coder", Command: "cat", Prompt: "You implement."},
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
	m := &Model{panes: panes}
	// force the settle preconditions so bootPanes fires immediately
	past := time.Now().Add(-5 * time.Second)
	for _, e := range panes {
		e.startedAt = past
		e.lastActive = past
	}
	m.bootPanes()

	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "orchestrator-context.md") }) {
		t.Fatalf("orchestrator boot one-liner not injected:\n%q", panes[0].pane.Render())
	}
	if !waitFor(func() bool { return strings.Contains(panes[1].pane.Render(), "coder-brief.md") }) {
		t.Fatalf("coder boot one-liner not injected:\n%q", panes[1].pane.Render())
	}
	ctx, err := os.ReadFile(filepath.Join(".choragos", "orchestrator-context.md"))
	if err != nil || !strings.Contains(string(ctx), "Delegation protocol") {
		t.Fatalf("orchestrator context file bad: err=%v", err)
	}
	if !panes[0].booted || !panes[1].booted {
		t.Error("panes should be marked booted")
	}
	// second call must not re-inject
	m.bootPanes()
}

func waitFor(cond func() bool) bool {
	deadline := time.After(3 * time.Second)
	for {
		if cond() {
			return true
		}
		select {
		case <-deadline:
			return false
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func waitStream(t *testing.T, p *pane.Pane) {
	t.Helper()
	done := make(chan struct{})
	go func() { _ = p.Stream(nil); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}
}
