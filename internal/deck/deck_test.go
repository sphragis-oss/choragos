// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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
	m := &Model{
		session: &session{panes: panes},
		tree:    wm.New(0), keys: config.Keys{}.Defaulted(),
		autoFocus: true, sidebar: true, w: 160, h: 48,
	}
	m.wireSession()
	return m
}

func TestStartPanesSpawnsAllRoles(t *testing.T) {
	t.Chdir(t.TempDir()) // isolate the per-role .choragos/logs
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
	th := themeFrom(config.Theme{})
	if s := computeStatus(&entry{lastActive: now}, now, th); s.dot != "●" || s.label != "working" || !s.working {
		t.Errorf("recent = %q/%q, want working", s.dot, s.label)
	}
	if s := computeStatus(&entry{lastActive: now.Add(-10 * time.Second)}, now, th); s.label != "idle 10s ago" {
		t.Errorf("stale label = %q", s.label)
	}
	if s := computeStatus(&entry{exited: true}, now, th); s.dot != "○" || s.label != "exited" || !s.exited {
		t.Errorf("exited = %q/%q", s.dot, s.label)
	}
}

func TestThemeFromOverrides(t *testing.T) {
	th := themeFrom(config.Theme{})
	if th.accent != "45" || th.working != "42" || th.waiting != "214" || th.scroll != "213" || th.idle != "244" || th.dim != "240" {
		t.Errorf("defaults = %+v", th)
	}
	th = themeFrom(config.Theme{Accent: "#ff00ff", Dim: "236"})
	if th.accent != "#ff00ff" || th.dim != "236" {
		t.Errorf("overrides = %+v", th)
	}
	if th.working != "42" || th.waiting != "214" {
		t.Errorf("unset keys changed: %+v", th)
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

func TestDelegateCheckpointsWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	panes := startCatPanes(t, "orchestrator", "coder") // note: chdirs into its own temp dir
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile("work.txt", []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(panes)
	m.initCheckpoints()
	if m.ckpt == nil {
		t.Fatal("checkpoints inactive in a git repo")
	}
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "T1 do work"})
	out, err := exec.Command("git", "for-each-ref", "--format=%(refname)", "refs/choragos/checkpoints").Output()
	if err != nil {
		t.Fatal(err)
	}
	refs := strings.TrimSpace(string(out))
	if !strings.HasSuffix(refs, "-T1") || strings.Contains(refs, "\n") {
		t.Fatalf("want exactly one T1 checkpoint ref, got %q", refs)
	}
}

func TestBoardRollbackRestoresWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	panes := startCatPanes(t, "orchestrator", "coder") // note: chdirs into its own temp dir
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile("work.txt", []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(panes)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	m.initCheckpoints()
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "T1 do work"})
	// the worker wrecks the workspace
	if err := os.WriteFile("work.txt", []byte("WRECKED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("junk.txt", []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.boardOn = true
	m.Update(key("u"))
	if !m.rbOn || m.rbMsg != "" {
		t.Fatalf("overlay: on=%v msg=%q", m.rbOn, m.rbMsg)
	}
	if m.rbFiles != 1 || len(m.rbExtra) != 1 {
		t.Fatalf("plan = %d restored / %d deleted, want 1/1", m.rbFiles, len(m.rbExtra))
	}
	if v := m.View(); !strings.Contains(v, "workspace rollback") || !strings.Contains(v, "roll back") {
		t.Fatal("rollback overlay not rendered")
	}
	// cancel: overlay closes, board stays, files untouched
	m.Update(key("q"))
	if m.rbOn || !m.boardOn {
		t.Fatal("cancel should return to the board")
	}
	if b, _ := os.ReadFile("work.txt"); string(b) != "WRECKED" {
		t.Fatalf("cancel touched files: %q", b)
	}
	// confirm: files restored, extras deleted, result shown until the next key
	m.Update(key("u"))
	m.Update(key("y"))
	if !strings.Contains(m.rbMsg, "rolled back") {
		t.Fatalf("result = %q", m.rbMsg)
	}
	if b, _ := os.ReadFile("work.txt"); string(b) != "precious" {
		t.Fatalf("work.txt = %q, want precious", b)
	}
	if _, err := os.Stat("junk.txt"); !os.IsNotExist(err) {
		t.Fatal("junk.txt should be deleted")
	}
	m.Update(key("x"))
	if m.rbOn || !m.boardOn {
		t.Fatal("closing the result should return to the board")
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
	if len(m.board) != 1 || m.board[0].id != "T1" || !m.board[0].doneAt.IsZero() {
		t.Fatalf("delegate event: %+v", m.board)
	}
	// the injected task file carries the id and the work-done echo instruction
	body, err := os.ReadFile(filepath.Join(".choragos", "worker-task-coder.md"))
	if err != nil || !strings.Contains(string(body), "work-done --id T1") {
		t.Fatalf("task file missing id echo: err=%v", err)
	}
	m.Update(key("ctrl+b"))
	m.Update(key("t"))
	if !m.boardOn {
		t.Fatal("prefix+t should open the task board")
	}
	if v := m.View(); !strings.Contains(v, "pending") {
		t.Fatal("unresolved delegation should show pending")
	}
	m.Update(key("q"))

	m.dispatch(ipc.Command{Cmd: "work-done", Task: "BUILD-7 fixed", Done: true, ID: "T1"})
	if len(m.board) != 2 || m.board[0].doneAt.IsZero() {
		t.Fatalf("work-done did not resolve T1: %+v", m.board)
	}
	m.Update(key("ctrl+b"))
	m.Update(key("t"))
	v := m.View()
	for _, want := range []string{"task board", "T1", "delegate", "coder", "BUILD-7 fix the flake", "work-done ✓", "✓ "} {
		if !strings.Contains(v, want) {
			t.Fatalf("board missing %q", want)
		}
	}
	if strings.Contains(v, "pending") {
		t.Fatal("resolved delegation still shows pending")
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

func FuzzChromeLine(f *testing.F) {
	for _, seed := range []string{"", "● working", "[████░░] 3%", "for shortcuts", "plain output", "⠋ spinner", strings.Repeat("─", 200)} {
		f.Add(seed)
	}
	f.Fuzz(func(_ *testing.T, s string) {
		_ = chromeLine(s, nil)
		_ = chromeLine(s, []string{s})
	})
}

func FuzzCollapseRepeat(f *testing.F) {
	for _, seed := range []string{"", "h", "hhh", "left", "παπ", "\x00\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		key, reps := collapseRepeat(s)
		if reps < 1 {
			t.Fatalf("reps = %d for %q", reps, s)
		}
		if s != "" && key == "" {
			t.Fatalf("non-empty input %q collapsed to empty key", s)
		}
	})
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
	// two TARGET blocks ~40 rows apart: PTY chunking may merge an echo row with
	// cat's copy, so occurrence COUNTS are unreliable but block positions are not
	put := func(s string) { _ = m.panes[0].pane.Input([]byte(s + "\r")) }
	for i := 0; i < 40; i++ {
		put(fmt.Sprintf("pad-a-%02d", i))
	}
	put("TARGET-ALPHA")
	for i := 0; i < 40; i++ {
		put(fmt.Sprintf("pad-b-%02d", i))
	}
	put("TARGET-ALPHA")
	for i := 0; i < 40; i++ {
		put(fmt.Sprintf("pad-c-%02d", i))
	}
	if !waitFor(func() bool { return strings.Contains(m.panes[0].pane.Render(), "pad-c-39") }) {
		t.Fatal("pane never echoed")
	}
	// cat's copies still stream after the echo; settle so offsets cannot shift mid-search
	if !waitFor(func() bool {
		s := m.panes[0].pane.Seq()
		time.Sleep(50 * time.Millisecond)
		return m.panes[0].pane.Seq() == s
	}) {
		t.Fatal("pane never settled")
	}
	m.Update(key("ctrl+b"))
	m.Update(key("/"))
	if !m.searching {
		t.Fatal("prefix+/ should enter search input")
	}
	if !strings.Contains(m.View(), "[SEARCH /") {
		t.Fatal("search indicator missing")
	}
	m.Update(key("TARGET-ALPHA"))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.searching {
		t.Fatal("enter should commit the query")
	}
	if m.scrollOff == 0 {
		t.Fatal("search should jump into scrollback")
	}
	if !strings.Contains(m.View(), "TARGET-ALPHA") {
		t.Fatal("match not brought into view")
	}
	// n walks strictly older; the first block guarantees a match further up
	prev := m.scrollOff
	m.Update(key("n"))
	if m.scrollOff <= prev {
		t.Fatalf("n should move further back: %d <= %d", m.scrollOff, prev)
	}
	// N walks back toward newer matches
	afterN := m.scrollOff
	m.Update(key("N"))
	if m.scrollOff >= afterN {
		t.Fatalf("N should move toward newer matches: %d >= %d", m.scrollOff, afterN)
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
	// raw output never steals focus (real agents redraw spinners constantly)
	m.Update(frameMsg{idx: 1})
	if m.active != 0 {
		t.Fatalf("frame stole focus: active = %d, want 0", m.active)
	}
	// a hidden role blocking on input steals focus when auto_focus is on
	_ = m.panes[1].pane.Input([]byte("Do you want to proceed? (y/n)\r"))
	if !waitFor(func() bool { return needsInput(m.panes[1]) }) {
		t.Fatal("pane never showed the blocking prompt")
	}
	m.checkWaiting()
	if m.active != 1 || m.tree.FocusedRole() != 1 {
		t.Fatalf("auto-focus on: active=%d focused=%d, want 1/1", m.active, m.tree.FocusedRole())
	}
	// off: an input prompt never steals focus
	m.autoFocus = false
	_ = m.panes[0].pane.Input([]byte("Do you want to proceed? (y/n)\r"))
	if !waitFor(func() bool { return needsInput(m.panes[0]) }) {
		t.Fatal("pane never showed the blocking prompt")
	}
	m.checkWaiting()
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

func TestAutoRestartOnFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	panes, err := startPanes(config.Config{Roles: []config.Role{
		{Name: "worker", Command: "sh", Args: []string{"-c", "exit 7"}, Restart: "on-failure", RestartRetries: 2},
	}}, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel(panes)
	e := panes[0]
	defer func() { _ = e.pane.Close() }()

	waitExit := func() {
		done := make(chan struct{})
		p := e.pane
		go func() { _ = p.Stream(nil); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("child did not exit")
		}
	}

	waitExit()
	first := e.pane
	m.Update(paneClosedMsg{idx: 0, gen: 0})
	if e.pane == first || e.exited || e.restarts != 1 {
		t.Fatalf("first failure: respawned=%v exited=%v restarts=%d", e.pane != first, e.exited, e.restarts)
	}
	waitExit()
	m.Update(paneClosedMsg{idx: 0, gen: e.gen})
	if e.exited || e.restarts != 2 {
		t.Fatalf("second failure: exited=%v restarts=%d, want running/2", e.exited, e.restarts)
	}
	waitExit()
	m.Update(paneClosedMsg{idx: 0, gen: e.gen})
	if !e.exited || e.restarts != 2 {
		t.Fatalf("cap: exited=%v restarts=%d, want exited/2", e.exited, e.restarts)
	}
}

func TestNoRestartOnCleanExitOrWithoutOptIn(t *testing.T) {
	t.Chdir(t.TempDir())
	panes, err := startPanes(config.Config{Roles: []config.Role{
		{Name: "clean", Command: "sh", Args: []string{"-c", "exit 0"}, Restart: "on-failure"},
		{Name: "default", Command: "sh", Args: []string{"-c", "exit 7"}},
	}}, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel(panes)
	for i, e := range panes {
		done := make(chan struct{})
		p := e.pane
		go func() { _ = p.Stream(nil); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("child did not exit")
		}
		m.Update(paneClosedMsg{idx: i, gen: 0})
		if !e.exited || e.restarts != 0 {
			t.Fatalf("pane %d: exited=%v restarts=%d, want exited/0", i, e.exited, e.restarts)
		}
	}
}

func TestApprovalGateFlow(t *testing.T) {
	t.Chdir(t.TempDir())
	panes, err := startPanes(config.Config{Roles: []config.Role{
		{Name: "orchestrator", Command: "cat", Start: true},
		{Name: "coder", Command: "cat", Approve: true},
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

	// gated: nothing reaches the worker, the gate is queued and visible
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "GATED-1 risky change"})
	if len(m.gates) != 1 || len(m.board) != 0 {
		t.Fatalf("gates=%d board=%d, want 1/0", len(m.gates), len(m.board))
	}
	time.Sleep(150 * time.Millisecond)
	if strings.Contains(panes[1].pane.Render(), "worker-task-coder.md") {
		t.Fatal("gated delegation reached the worker before approval")
	}
	if got := m.renderGate(60, 20); !strings.Contains(got, "coder") || !strings.Contains(got, "GATED-1") {
		t.Fatalf("gate overlay missing details:\n%q", got)
	}

	// approve: the delegation is injected and recorded
	m.Update(key("y"))
	if len(m.gates) != 0 || len(m.board) != 1 {
		t.Fatalf("after approve: gates=%d board=%d, want 0/1", len(m.gates), len(m.board))
	}
	if !waitFor(func() bool { return strings.Contains(panes[1].pane.Render(), "worker-task-coder.md") }) {
		t.Fatalf("approved delegation not injected:\n%q", panes[1].pane.Render())
	}

	// reject: the orchestrator hears about it, the worker does not
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "GATED-2 other change"})
	m.Update(key("x")) // unknown key: gate stays
	if len(m.gates) != 1 {
		t.Fatalf("unknown key resolved the gate: gates=%d", len(m.gates))
	}
	m.Update(key("n"))
	if len(m.gates) != 0 {
		t.Fatalf("after reject: gates=%d, want 0", len(m.gates))
	}
	// the 40-col pane wraps the line, so match a fragment that fits one row
	if !waitFor(func() bool { return strings.Contains(panes[0].pane.Render(), "The user rejected") }) {
		t.Fatalf("rejection not injected into orchestrator:\n%q", panes[0].pane.Render())
	}
	if strings.Contains(panes[1].pane.Render(), "GATED-2") {
		t.Fatal("rejected delegation reached the worker")
	}
}

func TestGateEditKey(t *testing.T) {
	t.Chdir(t.TempDir())
	panes, err := startPanes(config.Config{Roles: []config.Role{
		{Name: "orchestrator", Command: "cat", Start: true},
		{Name: "coder", Command: "cat", Approve: true},
	}}, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, e := range panes {
			_ = e.pane.Close()
		}
	}()
	m := newTestModel(panes)

	// without a brief: e is inert, the footer does not advertise it
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "GATED-NOBRIEF"})
	if got := m.renderGate(60, 20); strings.Contains(got, "[e] edit brief") {
		t.Fatalf("footer advertises edit without a brief:\n%q", got)
	}
	if _, cmd := m.Update(key("e")); cmd != nil || len(m.gates) != 1 {
		t.Fatalf("e without brief: cmd=%v gates=%d, want nil/1", cmd, len(m.gates))
	}
	m.gates = nil

	// with a brief: e returns the editor exec and the gate stays pending
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "GATED-BRIEF", Brief: "/tmp/brief.md"})
	if got := m.renderGate(60, 20); !strings.Contains(got, "[e] edit brief") {
		t.Fatalf("footer missing edit key with a brief:\n%q", got)
	}
	if _, cmd := m.Update(key("e")); cmd == nil {
		t.Fatal("e with brief returned no editor command")
	}
	if len(m.gates) != 1 {
		t.Fatalf("edit resolved the gate: gates=%d, want 1", len(m.gates))
	}
}

func TestOnGateHookFires(t *testing.T) {
	t.Chdir(t.TempDir())
	panes, err := startPanes(config.Config{Roles: []config.Role{
		{Name: "orchestrator", Command: "cat", Start: true},
		{Name: "reviewer", Command: "cat", Approve: true},
	}}, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, e := range panes {
			_ = e.pane.Close()
		}
	}()
	m := newTestModel(panes)
	out := filepath.Join(t.TempDir(), "hook.out")
	m.cfg.UI.OnGate = "echo gate:$CHORAGOS_ROLE:$CHORAGOS_TASK >> " + out

	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"reviewer"}, Task: "RISKY-1"})
	if !waitFor(func() bool {
		b, err := os.ReadFile(out)
		return err == nil && strings.Contains(string(b), "gate:reviewer:RISKY-1")
	}) {
		t.Fatal("on_gate hook never fired with role and task in env")
	}

	// ungated delegation: no hook
	m.gates = nil
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"orchestrator"}, Task: "PLAIN-1"})
	time.Sleep(150 * time.Millisecond)
	if b, _ := os.ReadFile(out); strings.Contains(string(b), "PLAIN-1") {
		t.Fatalf("hook fired for an ungated delegation:\n%s", b)
	}
}

func TestOnInputHookFiresOncePerEdge(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator"))
	out := filepath.Join(t.TempDir(), "hook.out")
	m.cfg.UI.OnInput = "echo input:$CHORAGOS_ROLE >> " + out
	_ = m.panes[0].pane.Input([]byte("Do you want to proceed? (y/n)\r"))
	if !waitFor(func() bool { return needsInput(m.panes[0]) }) {
		t.Fatal("pane never showed the blocking prompt")
	}
	m.checkWaiting()
	m.checkWaiting() // still waiting: no second fire
	if !waitFor(func() bool {
		b, err := os.ReadFile(out)
		return err == nil && strings.Contains(string(b), "input:orchestrator")
	}) {
		t.Fatal("on_input hook never fired")
	}
	time.Sleep(150 * time.Millisecond)
	if b, _ := os.ReadFile(out); strings.Count(string(b), "input:orchestrator") != 1 {
		t.Fatalf("hook fired more than once per edge:\n%s", b)
	}
}

// reloadFixture starts a deck from a real config file so reloadConfig can re-read it.
func reloadFixture(t *testing.T, body string) (*Model, string) {
	t.Helper()
	t.Chdir(t.TempDir())
	path := filepath.Join(".", "team.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	panes, err := startPanes(cfg, 40, 6, "", "")
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
	m.cfg = cfg
	return m, path
}

const reloadBase = `[[roles]]
name = "orchestrator"
command = "cat"
start = true

[[roles]]
name = "coder"
command = "cat"
`

func TestReloadAddsAndRemovesRoles(t *testing.T) {
	m, path := reloadFixture(t, reloadBase)
	next := `[[roles]]
name = "orchestrator"
command = "cat"
start = true

[[roles]]
name = "reviewer"
command = "cat"
`
	if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
		t.Fatal(err)
	}
	m.reloadConfig()

	if len(m.panes) != 3 {
		t.Fatalf("panes = %d, want 3 (tombstones are kept)", len(m.panes))
	}
	if e, _ := m.findRole("coder"); e != nil {
		t.Fatal("removed role still resolvable")
	}
	if !m.panes[1].gone || !m.panes[1].exited {
		t.Fatalf("coder not tombstoned: gone=%v exited=%v", m.panes[1].gone, m.panes[1].exited)
	}
	e, i := m.findRole("reviewer")
	if e == nil || i != 2 {
		t.Fatalf("added role not appended: %v idx=%d", e, i)
	}
	// the new role is a live delegation target
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"reviewer"}, Task: "NEW-1"})
	if !waitFor(func() bool { return strings.Contains(e.pane.Render(), "worker-task-reviewer.md") }) {
		t.Fatal("delegation to the added role not injected")
	}
	// the tombstone is never offered by split: auto-focus moved to reviewer, so orchestrator is the hidden one
	if got := m.nextHiddenRole(); got != 0 {
		t.Fatalf("nextHiddenRole = %d, want 0 (never the tombstone)", got)
	}
	// the orchestrator hears about the roster change
	if !waitFor(func() bool { return strings.Contains(m.panes[0].pane.Render(), "Team changed") }) {
		t.Fatal("roster notice not injected into the orchestrator")
	}
}

func TestReloadRespawnsChangedSpec(t *testing.T) {
	m, path := reloadFixture(t, reloadBase)
	next := strings.Replace(reloadBase, "name = \"coder\"\ncommand = \"cat\"",
		"name = \"coder\"\ncommand = \"sh\"\nargs = [\"-c\", \"printf coder-respawned; exec cat\"]", 1)
	if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
		t.Fatal(err)
	}
	gen := m.panes[1].gen
	m.reloadConfig()
	if len(m.panes) != 2 {
		t.Fatalf("panes = %d, want 2", len(m.panes))
	}
	if m.panes[1].gen != gen+1 {
		t.Fatalf("gen = %d, want %d (respawn)", m.panes[1].gen, gen+1)
	}
	if !waitFor(func() bool { return strings.Contains(m.panes[1].pane.Render(), "coder-respawned") }) {
		t.Fatal("respawned process not running the new command")
	}
}

func TestReloadSoftChangeKeepsProcess(t *testing.T) {
	m, path := reloadFixture(t, reloadBase)
	next := reloadBase + "\n" // same specs, coder gains a gate and a prompt
	next = strings.Replace(next, "name = \"coder\"\ncommand = \"cat\"",
		"name = \"coder\"\ncommand = \"cat\"\napprove = true\nprompt_template = \"new brief\"", 1)
	if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
		t.Fatal(err)
	}
	gen := m.panes[1].gen
	m.reloadConfig()
	if m.panes[1].gen != gen {
		t.Fatal("soft field change restarted the process")
	}
	if !m.panes[1].role.Approve || m.panes[1].role.Prompt != "new brief" {
		t.Fatalf("soft fields not applied: %+v", m.panes[1].role)
	}
	// the new gate is live
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "GATED-NOW"})
	if len(m.gates) != 1 {
		t.Fatalf("gates = %d, want 1 after approve turned on", len(m.gates))
	}
}

func TestReloadProtectsStartRoleAndInFlight(t *testing.T) {
	m, path := reloadFixture(t, reloadBase)
	// an unresolved delegation to coder blocks its respawn
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "INFLIGHT-1"})
	next := `[[roles]]
name = "orchestrator"
command = "sh"
args = ["-c", "exec cat"]
start = true

[[roles]]
name = "coder"
command = "sh"
args = ["-c", "exec cat"]
`
	if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
		t.Fatal(err)
	}
	genO, genC := m.panes[0].gen, m.panes[1].gen
	m.reloadConfig()
	if m.panes[0].gen != genO {
		t.Fatal("start role was respawned by reload")
	}
	if m.panes[0].role.Command != "cat" {
		t.Fatalf("start role spec replaced: %q", m.panes[0].role.Command)
	}
	if m.panes[1].gen != genC {
		t.Fatal("in-flight role was respawned by reload")
	}
}

func TestReloadRefusedWithoutConfigFile(t *testing.T) {
	m := newTestModel(startCatPanes(t, "orchestrator"))
	m.cfg.Path = ""
	m.reloadConfig() // must not panic or mutate
	if len(m.panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(m.panes))
	}
}

func TestDelegateWithoutApproveIsImmediate(t *testing.T) {
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
	go func(p *pane.Pane) { _ = p.Stream(nil) }(panes[1].pane)
	m := newTestModel(panes)
	m.dispatch(ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "FAST-1"})
	if len(m.gates) != 0 {
		t.Fatalf("ungated delegate queued: gates=%d", len(m.gates))
	}
	if !waitFor(func() bool { return strings.Contains(panes[1].pane.Render(), "worker-task-coder.md") }) {
		t.Fatal("ungated delegation not injected")
	}
}
