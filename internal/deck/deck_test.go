// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
	"github.com/sphragis-oss/choragos/internal/wm"
)

// newTestModel wires a Model the way startAll does: focused single tile, defaults on.
func newTestModel(panes []*entry) *Model {
	return &Model{
		panes: panes, tree: wm.New(0), keys: config.Keys{}.Defaulted(),
		autoFocus: true, sidebar: true, w: 160, h: 48,
	}
}

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

func TestDims(t *testing.T) {
	// 120x40 with sidebar: left ~1/3, main the rest, one status row.
	m := &Model{w: 120, h: 40, sidebar: true}
	leftW, mainW, contentH := m.dims()
	if leftW != 40 || mainW != 80 || contentH != 39 {
		t.Errorf("dims = %d/%d/%d, want 40/80/39", leftW, mainW, contentH)
	}
	// sidebar hidden: main takes the full width.
	m.sidebar = false
	if leftW, mainW, _ = m.dims(); leftW != 0 || mainW != 120 {
		t.Errorf("no-sidebar dims = %d/%d, want 0/120", leftW, mainW)
	}
	// tiny terminal: everything stays positive.
	m = &Model{w: 3, h: 2, sidebar: true}
	if leftW, mainW, contentH = m.dims(); leftW < 1 || mainW < 1 || contentH < 1 {
		t.Errorf("tiny dims invalid: %d/%d/%d", leftW, mainW, contentH)
	}
}

func TestTileContent(t *testing.T) {
	if cw, ch, chrome := tileContent(80, 20); !chrome || cw != 78 || ch != 17 {
		t.Errorf("tileContent(80,20) = %d/%d/%v", cw, ch, chrome)
	}
	// tiny tile: no chrome, dims clamp positive.
	if cw, ch, chrome := tileContent(4, 0); chrome || cw != 4 || ch != 1 {
		t.Errorf("tileContent(4,0) = %d/%d/%v", cw, ch, chrome)
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
		if !promptInLines(lines, nil) {
			t.Errorf("expected blocking prompt in %q", lines)
		}
	}
	if promptInLines([]string{"Thought for 5s", "Read 1 file", "Standing by."}, nil) {
		t.Error("false positive on ordinary output")
	}
	// role-configured extras extend the built-in markers
	if !promptInLines([]string{"Gemini asks: continue? <enter>"}, []string{"continue? <enter>"}) {
		t.Error("extra marker not honored")
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
	got := activityTail(lines, 3, nil)
	want := []string{"My role is orchestrator.", "Read 1 file", "Ready for your instructions."}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if chromeLine("● Delegating the task to coder.", nil) {
		t.Error("a bulleted content line must not be treated as chrome")
	}
	if !chromeLine("mytui statusbar v2", []string{"mytui statusbar"}) {
		t.Error("extra chrome marker not honored")
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
	m := newTestModel(panes)

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
	m := newTestModel(panes)
	m.events = slog.New(slog.NewTextHandler(&buf, nil))
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
	m := newTestModel(panes)
	m.sphragisOn, m.gatewayUp = true, false
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
	m := newTestModel(panes)
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

func startCatPanes(t *testing.T, names ...string) []*entry {
	t.Helper()
	t.Chdir(t.TempDir()) // isolate the per-role .choragos/logs
	var roles []config.Role
	for i, n := range names {
		roles = append(roles, config.Role{Name: n, Command: "cat", Start: i == 0})
	}
	panes, err := startPanes(config.Config{Roles: roles}, 40, 6, "", "")
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

func key(s string) tea.KeyMsg {
	switch s {
	case "ctrl+b":
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestPrefixModeSplitAndFocus(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder", "reviewer"))

	// prefix arms WM mode without touching the layout
	m.Update(key("ctrl+b"))
	if !m.prefixed {
		t.Fatal("prefix should arm WM mode")
	}
	if len(m.tree.VisibleRoles()) != 1 {
		t.Fatal("arming must not change the layout")
	}
	// mapped key splits: a second role tile appears, focused
	m.Update(key("v"))
	if m.prefixed {
		t.Fatal("action must disarm prefix mode")
	}
	vis := m.tree.VisibleRoles()
	if len(vis) != 2 || vis[0] != 0 || vis[1] != 1 {
		t.Fatalf("split visible roles = %v, want [0 1]", vis)
	}
	if m.active != 1 {
		t.Fatalf("active = %d, want new tile 1", m.active)
	}
	// focus left moves geometrically
	m.Update(key("ctrl+b"))
	m.Update(key("h"))
	if m.active != 0 {
		t.Fatalf("focus left: active = %d, want 0", m.active)
	}
	// cycle next/prev wrap across visible tiles
	m.Update(key("ctrl+b"))
	m.Update(key("tab"))
	if m.active != 1 {
		t.Fatalf("cycle next: active = %d, want 1", m.active)
	}
	m.Update(key("ctrl+b"))
	m.Update(key("shift+tab"))
	if m.active != 0 {
		t.Fatalf("cycle prev: active = %d, want 0", m.active)
	}
	m.Update(key("ctrl+b"))
	m.Update(key("tab"))
	// unmapped key after prefix: no-op, exits prefix mode, no layout change
	m.Update(key("ctrl+b"))
	m.Update(key("q"))
	if m.prefixed || len(m.tree.VisibleRoles()) != 2 {
		t.Fatal("unmapped key must exit prefix mode without side effects")
	}
	// prefix byte and swallowed keys never reach the PTY
	time.Sleep(150 * time.Millisecond)
	for i, e := range m.panes {
		out := e.pane.Render()
		if strings.Contains(out, "q") || strings.Contains(out, "v") {
			t.Fatalf("pane %d saw a WM key: %q", i, out)
		}
	}
	// normal-mode keys still forward to the focused pane
	_, _ = m.Update(key("y"))
	if !waitFor(func() bool { return strings.Contains(m.panes[1].pane.Render(), "y") }) {
		t.Fatal("normal key did not reach the focused PTY")
	}
}

func TestViewTilesRenderSimultaneously(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder"))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	m.Update(key("ctrl+b"))
	m.Update(key("v"))
	_ = m.panes[0].pane.Input([]byte("alpha-out"))
	_ = m.panes[1].pane.Input([]byte("beta-out"))
	if !waitFor(func() bool {
		v := m.View()
		return strings.Contains(v, "alpha-out") && strings.Contains(v, "beta-out")
	}) {
		t.Fatal("both role panes should render live at once")
	}
}

func TestViewTinyTerminalNeverPanics(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder", "reviewer"))
	m.Update(key("ctrl+b"))
	m.Update(key("v"))
	m.Update(key("ctrl+b"))
	m.Update(key("-"))
	for _, dim := range [][2]int{{10, 3}, {1, 1}, {0, 0}, {5, 40}, {200, 2}} {
		m.w, m.h = dim[0], dim[1]
		m.resizePanes()
		if m.View() == "" {
			t.Fatalf("empty view at %v", dim)
		}
	}
}

func TestClosePaneKeepsAgentRunning(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder"))
	m.Update(key("ctrl+b"))
	m.Update(key("v")) // coder tile, focused
	m.Update(key("ctrl+b"))
	m.Update(key("x")) // close coder tile
	if vis := m.tree.VisibleRoles(); len(vis) != 1 || vis[0] != 0 {
		t.Fatalf("after close: visible = %v, want [0]", vis)
	}
	if m.panes[1].exited {
		t.Fatal("closing a tile must not kill the agent")
	}
	// hidden pane still accepts input (process alive)
	if err := m.panes[1].pane.Input([]byte("alive")); err != nil {
		t.Fatalf("hidden pane rejected input: %v", err)
	}
	if !waitFor(func() bool { return strings.Contains(m.panes[1].pane.Render(), "alive") }) {
		t.Fatal("hidden agent stopped echoing; process likely dead")
	}
	// re-addable via split
	m.Update(key("ctrl+b"))
	m.Update(key("-"))
	if vis := m.tree.VisibleRoles(); len(vis) != 2 {
		t.Fatalf("re-split: visible = %v", vis)
	}
	// closing the last tile is refused
	m2 := newTestModel(startCatPanes(t, "solo"))
	m2.Update(key("ctrl+b"))
	m2.Update(key("x"))
	if len(m2.tree.VisibleRoles()) != 1 {
		t.Fatal("last tile must survive close")
	}
}

func TestZoomAndResizeMode(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder"))
	m.Update(key("ctrl+b"))
	m.Update(key("v"))
	_, mainW, contentH := m.dims()
	before := m.tree.Layout(mainW, contentH)

	m.Update(key("ctrl+b"))
	m.Update(key("z"))
	if !m.tree.Zoomed() {
		t.Fatal("zoom did not engage")
	}
	if tiles := m.tree.Layout(mainW, contentH); len(tiles) != 1 || tiles[0].W != mainW {
		t.Fatalf("zoomed layout = %+v", tiles)
	}
	m.Update(key("ctrl+b"))
	m.Update(key("z"))
	after := m.tree.Layout(mainW, contentH)
	if len(after) != len(before) || after[0] != before[0] {
		t.Fatalf("zoom off did not restore the tree: %+v vs %+v", before, after)
	}

	// resize mode: h shrinks the first split, the focused pane reflows live, unmapped key exits
	paneW := lipgloss.Width(strings.SplitN(m.panes[1].pane.Render(), "\n", 2)[0])
	m.Update(key("ctrl+b"))
	m.Update(key("r"))
	if !m.tree.Resizing() {
		t.Fatal("resize mode did not engage")
	}
	m.Update(key("h"))
	tiles := m.tree.Layout(mainW, contentH)
	if tiles[0].W >= before[0].W {
		t.Fatalf("resize h did not shrink first tile: %d >= %d", tiles[0].W, before[0].W)
	}
	if got := lipgloss.Width(strings.SplitN(m.panes[1].pane.Render(), "\n", 2)[0]); got <= paneW {
		t.Fatalf("focused pane did not reflow: width %d <= %d", got, paneW)
	}
	m.Update(key("q"))
	if m.tree.Resizing() {
		t.Fatal("unmapped key must exit resize mode")
	}
}

func TestRoleEnvIsolation(t *testing.T) {
	t.Setenv("CHOR_TEST_SECRET", "s3cret")
	t.Setenv("CHOR_TEST_TOKEN", "tok")
	t.Setenv("AWS_TEST_KEY", "aws")
	has := func(env []string, name string) bool {
		for _, kv := range env {
			if strings.HasPrefix(kv, name+"=") {
				return true
			}
		}
		return false
	}
	// default: full env inherited
	env := roleEnv(config.Role{}, "/tmp/s.sock", "")
	if !has(env, "CHOR_TEST_SECRET") || !has(env, "PATH") || !has(env, "CHORAGOS_SOCK") {
		t.Fatal("default mode should inherit the full env plus the socket")
	}
	// env_deny strips exact names and prefix patterns
	env = roleEnv(config.Role{EnvDeny: []string{"CHOR_TEST_SECRET", "AWS_*"}}, "/tmp/s.sock", "")
	if has(env, "CHOR_TEST_SECRET") || has(env, "AWS_TEST_KEY") {
		t.Fatal("env_deny not applied")
	}
	if !has(env, "CHOR_TEST_TOKEN") {
		t.Fatal("env_deny must not strip unrelated vars")
	}
	// env_allow: baseline + allowed only
	env = roleEnv(config.Role{EnvAllow: []string{"CHOR_TEST_TOKEN"}}, "/tmp/s.sock", "http://gw")
	if has(env, "CHOR_TEST_SECRET") || has(env, "AWS_TEST_KEY") {
		t.Fatal("allowlist mode leaked non-allowed vars")
	}
	if !has(env, "CHOR_TEST_TOKEN") || !has(env, "PATH") || !has(env, "HOME") {
		t.Fatal("allowlist mode must keep baseline and allowed vars")
	}
	if !has(env, "CHORAGOS_SOCK") || !has(env, "ANTHROPIC_BASE_URL") {
		t.Fatal("choragos-injected vars must always be present")
	}
	// deny wins over allow
	env = roleEnv(config.Role{EnvAllow: []string{"CHOR_TEST_TOKEN"}, EnvDeny: []string{"CHOR_TEST_TOKEN"}}, "/tmp/s.sock", "")
	if has(env, "CHOR_TEST_TOKEN") {
		t.Fatal("env_deny must win over env_allow")
	}
}

func TestBootVerifyAndRetry(t *testing.T) {
	t.Chdir(t.TempDir())
	// role reads stdin but never echoes: the injection can never be verified on screen
	panes, err := startPanes(config.Config{Roles: []config.Role{
		{Name: "mute", Command: "sh", Args: []string{"-c", "stty -echo 2>/dev/null; cat >/dev/null"}},
	}}, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = panes[0].pane.Close() }()
	go func() { _ = panes[0].pane.Stream(nil) }()
	m := newTestModel(panes)
	time.Sleep(500 * time.Millisecond) // let stty -echo take effect before injecting
	past := time.Now().Add(-5 * time.Second)
	panes[0].startedAt, panes[0].lastActive = past, past

	m.bootPanes() // injects, tries=1
	e := panes[0]
	if !e.booted || e.bootTries != 1 || e.bootLine == "" {
		t.Fatalf("first boot: booted=%v tries=%d", e.booted, e.bootTries)
	}
	time.Sleep(100 * time.Millisecond) // let any echo surface before verify
	m.bootPanes()                      // too early to retry
	if e.bootTries != 1 {
		t.Fatalf("retried before bootVerifyAfter: tries=%d", e.bootTries)
	}
	e.bootSentAt = past // force the verify window
	m.bootPanes()       // unverified: retry once
	if e.bootTries != 2 || e.bootOK {
		t.Fatalf("expected retry: tries=%d ok=%v", e.bootTries, e.bootOK)
	}
	e.bootSentAt = past
	m.bootPanes() // exhausted: give up quietly
	if !e.bootOK {
		t.Fatal("should give up after bootMaxTries")
	}
}

func TestBootVerifiedStopsRetries(t *testing.T) {
	t.Chdir(t.TempDir())
	m := newTestModel(startCatPanes(t, "orchestrator"))
	e := m.panes[0]
	past := time.Now().Add(-5 * time.Second)
	e.startedAt, e.lastActive = past, past
	m.bootPanes()
	if !waitFor(func() bool { return bootLanded(e) }) {
		t.Fatalf("echoed injection should verify:\n%q", e.pane.Render())
	}
	e.bootSentAt = past
	m.bootPanes()
	if !e.bootOK || e.bootTries != 1 {
		t.Fatalf("verified boot must not retry: ok=%v tries=%d", e.bootOK, e.bootTries)
	}
}

func TestRestartRole(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator"))
	old := m.panes[0].pane
	m.panes[0].exited = true
	m.panes[0].booted = true
	m.Update(key("ctrl+b"))
	m.Update(key("R"))
	e := m.panes[0]
	if e.pane == old {
		t.Fatal("restart should spawn a fresh pane")
	}
	if e.exited || e.booted || e.gen != 1 {
		t.Fatalf("restart state: exited=%v booted=%v gen=%d", e.exited, e.booted, e.gen)
	}
	// watchPane (called by restartRole) already streams the new pane
	_ = e.pane.Input([]byte("reborn"))
	if !waitFor(func() bool { return strings.Contains(e.pane.Render(), "reborn") }) {
		t.Fatal("restarted pane not echoing")
	}
	// a stale close message from the old stream must be dropped
	m.Update(paneClosedMsg{idx: 0, gen: 0})
	if e.exited {
		t.Fatal("stale paneClosedMsg marked the new pane exited")
	}
	m.Update(paneClosedMsg{idx: 0, gen: 1})
	if !e.exited {
		t.Fatal("current-gen paneClosedMsg should mark exited")
	}
	_ = e.pane.Close()
}

func TestTaskBoardRecordsAndRenders(t *testing.T) {
	t.Chdir(t.TempDir())
	m := newTestModel(startCatPanes(t, "orchestrator", "coder"))
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "BUILD-7 fix the flake"})
	m.dispatch(ipc.Command{Cmd: "work-done", Task: "BUILD-7 fixed", Done: true})
	if len(m.board) != 2 {
		t.Fatalf("board events = %d, want 2", len(m.board))
	}
	m.Update(key("ctrl+b"))
	m.Update(key("t"))
	if !m.boardOn {
		t.Fatal("prefix+t should open the task board")
	}
	v := m.View()
	for _, want := range []string{"task board", "delegate", "coder", "BUILD-7 fix the flake", "work-done ✓"} {
		if !strings.Contains(v, want) {
			t.Fatalf("board missing %q", want)
		}
	}
	m.Update(key("q"))
	if m.boardOn {
		t.Fatal("any key should close the board")
	}
}

func TestMouseClickFocusesTile(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder"))
	m.Update(key("ctrl+b"))
	m.Update(key("v")) // two tiles; focus on coder (right)
	if m.active != 1 {
		t.Fatalf("setup: active = %d", m.active)
	}
	leftW, _, _ := m.dims()
	// click inside the left tile (just right of the sidebar)
	m.Update(tea.MouseMsg{X: leftW + 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 0 {
		t.Fatalf("click should focus the left tile, active = %d", m.active)
	}
	// click on the sidebar is inert
	m.Update(tea.MouseMsg{X: 1, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 0 {
		t.Fatal("sidebar click must not change focus")
	}
	// wheel drives scrollback
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.scrollOff != scrollStep {
		t.Fatalf("wheel up: scrollOff = %d", m.scrollOff)
	}
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.scrollOff != 0 {
		t.Fatalf("wheel down: scrollOff = %d", m.scrollOff)
	}
}

func TestWaitingBellEdgeTriggered(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator"))
	rings := 0
	m.bellFn = func() { rings++ }
	_ = m.panes[0].pane.Input([]byte("Do you want to proceed? (y/n)\r"))
	if !waitFor(func() bool { return needsInput(m.panes[0]) }) {
		t.Fatal("pane never showed the blocking prompt")
	}
	m.checkWaiting()
	m.checkWaiting() // still waiting: no second ring
	if rings != 1 {
		t.Fatalf("rings = %d, want exactly 1", rings)
	}
	if !m.panes[0].waiting {
		t.Fatal("waiting state not recorded")
	}
}

func TestBroadcastMode(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder"))
	m.Update(key("ctrl+b"))
	m.Update(key("a"))
	if !m.broadcast {
		t.Fatal("prefix+a should enable broadcast")
	}
	m.Update(key("y"))
	for i, e := range m.panes {
		p := e.pane
		if !waitFor(func() bool { return strings.Contains(p.Render(), "y") }) {
			t.Fatalf("pane %d missed broadcast input", i)
		}
	}
	m.Update(key("ctrl+b"))
	m.Update(key("a"))
	if m.broadcast {
		t.Fatal("prefix+a should toggle broadcast off")
	}
	m.Update(key("z"))
	time.Sleep(150 * time.Millisecond)
	if strings.Contains(m.panes[1].pane.Render(), "z") && m.active != 1 {
		t.Fatal("broadcast off: unfocused pane still received input")
	}
}

func TestCollapseRepeat(t *testing.T) {
	cases := []struct {
		in   string
		key  string
		reps int
	}{
		{"h", "h", 1}, {"hhh", "h", 3}, {"left", "left", 1}, {"jk", "jk", 1},
	}
	for _, c := range cases {
		if k, n := collapseRepeat(c.in); k != c.key || n != c.reps {
			t.Errorf("collapseRepeat(%q) = %q/%d, want %q/%d", c.in, k, n, c.key, c.reps)
		}
	}
}

func TestResizeModeSurvivesKeyRepeat(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder"))
	m.Update(key("ctrl+b"))
	m.Update(key("v"))
	_, mainW, contentH := m.dims()
	before := m.tree.Layout(mainW, contentH)[0].W
	m.Update(key("ctrl+b"))
	m.Update(key("r"))
	m.Update(key("hhh")) // coalesced key-repeat run
	if !m.tree.Resizing() {
		t.Fatal("coalesced repeat must not exit resize mode")
	}
	if got := m.tree.Layout(mainW, contentH)[0].W; got >= before {
		t.Fatalf("repeat run did not resize: %d >= %d", got, before)
	}
}

func TestHelpOverlay(t *testing.T) {
	m := newTestModel(startCatPanes(t, "solo", "coder"))
	m.Update(key("ctrl+b"))
	m.Update(key("?"))
	if !m.helpOn {
		t.Fatal("prefix+? should open help")
	}
	if v := m.View(); !strings.Contains(v, "keybindings") {
		t.Fatal("help overlay not rendered")
	}
	m.Update(key("w"))
	if m.helpOn {
		t.Fatal("any key should close help")
	}
	time.Sleep(150 * time.Millisecond)
	if strings.Contains(m.panes[0].pane.Render(), "w") {
		t.Fatal("help-closing key must not reach the PTY")
	}
}

func TestToggleSidebarReflows(t *testing.T) {
	m := newTestModel(startCatPanes(t, "solo"))
	_, mainBefore, _ := m.dims()
	m.Update(key("ctrl+b"))
	m.Update(key("b"))
	if m.sidebar {
		t.Fatal("sidebar should hide")
	}
	if _, mainAfter, _ := m.dims(); mainAfter <= mainBefore {
		t.Fatalf("main area should widen: %d <= %d", mainAfter, mainBefore)
	}
	m.Update(key("ctrl+b"))
	m.Update(key("b"))
	if !m.sidebar {
		t.Fatal("sidebar should show again")
	}
}

func TestScrollbackOnFocusedTile(t *testing.T) {
	m := newTestModel(startCatPanes(t, "solo"))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	for i := 0; i < 80; i++ {
		_ = m.panes[0].pane.Input([]byte(fmt.Sprintf("scroll-me-%d\r", i)))
	}
	if !waitFor(func() bool { return strings.Contains(m.panes[0].pane.Render(), "scroll-me-79") }) {
		t.Fatal("pane never echoed")
	}
	m.Update(key("pgup"))
	if m.scrollOff != scrollStep {
		t.Fatalf("scrollOff = %d, want %d", m.scrollOff, scrollStep)
	}
	if v := m.View(); !strings.Contains(v, "scrollback") {
		t.Fatal("status line should show the scrollback indicator")
	}
}

func TestScrollbackSearch(t *testing.T) {
	m := newTestModel(startCatPanes(t, "solo"))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	for i := 0; i < 120; i++ {
		_ = m.panes[0].pane.Input([]byte(fmt.Sprintf("needle-%03d\r", i)))
	}
	if !waitFor(func() bool { return strings.Contains(m.panes[0].pane.Render(), "needle-119") }) {
		t.Fatal("pane never echoed")
	}
	m.Update(key("ctrl+b"))
	m.Update(key("/"))
	if !m.searching {
		t.Fatal("prefix+/ should enter search input")
	}
	if !strings.Contains(m.View(), "[SEARCH /") {
		t.Fatal("search indicator missing")
	}
	m.Update(key("needle-005"))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.searching {
		t.Fatal("enter should commit the query")
	}
	if m.scrollOff == 0 {
		t.Fatal("search should jump into scrollback")
	}
	if !strings.Contains(m.View(), "needle-005") {
		t.Fatal("match not brought into view")
	}
	// cat echoes and re-emits every line, so a second occurrence exists further up
	prev := m.scrollOff
	m.Update(key("n"))
	if m.scrollOff <= prev {
		t.Fatalf("n should move further back: %d <= %d", m.scrollOff, prev)
	}
	m.Update(key("N"))
	if m.scrollOff != prev {
		t.Fatalf("N should return to the newer match: %d != %d", m.scrollOff, prev)
	}
	// esc cancels input mode without committing
	m.Update(key("ctrl+b"))
	m.Update(key("/"))
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.searching {
		t.Fatal("esc should cancel search input")
	}
}

func TestAutoFocusModes(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator", "coder"))
	// activity on a hidden role steals focus when auto_focus is on
	m.Update(frameMsg{idx: 1})
	if m.active != 1 || m.tree.FocusedRole() != 1 {
		t.Fatalf("auto-focus on: active=%d focused=%d, want 1/1", m.active, m.tree.FocusedRole())
	}
	// off: activity never steals focus
	m.autoFocus = false
	m.Update(frameMsg{idx: 0})
	if m.active != 1 {
		t.Fatalf("auto-focus off: active = %d, want 1", m.active)
	}
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
