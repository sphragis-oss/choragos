// SPDX-License-Identifier: Apache-2.0

// Package deck is the Bubble Tea TUI: a toggleable status-card sidebar plus a tiling window manager over the role panes.
package deck

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
	"github.com/sphragis-oss/choragos/internal/prompt"
	"github.com/sphragis-oss/choragos/internal/sphragis"
	"github.com/sphragis-oss/choragos/internal/wm"
)

// contextDir holds the generated role prompts, referenced by the injected one-liners.
const contextDir = ".choragos"

// boot injection fires once a pane goes quiet (bootSettle) after starting (bootMinWait): agent TUI ready.
const (
	bootMinWait = 1 * time.Second
	bootSettle  = 1200 * time.Millisecond
)

// injectEnterDelay separates the typed text from its submit; Claude's TUI treats text+Enter in one write as a paste.
const injectEnterDelay = 200 * time.Millisecond

// gracefulTimeout is how long a quit waits for agents to exit on SIGTERM (running SessionEnd hooks) before force-killing.
const gracefulTimeout = 5 * time.Second

const (
	minSidebar                       = 24
	cardActivityLines                = 3     // tail rows previewed per status card
	scrollStep                       = 5     // rows moved per PgUp/PgDn
	accentColor       lipgloss.Color = "45"  // focused: cyan
	workingColor      lipgloss.Color = "42"  // working: green
	waitingColor      lipgloss.Color = "214" // blocked on user input: orange
	scrollColor       lipgloss.Color = "213" // scrollback active: magenta
	idleColor         lipgloss.Color = "244" // idle: grey
	dimColor          lipgloss.Color = "240" // exited/unfocused
	workingWindow                    = 2 * time.Second
)

// resizeStep is the ratio delta per keypress in resize mode.
const resizeStep = 0.05

// frameMsg signals a pane produced new output; gen guards against a restarted role's stale stream.
type frameMsg struct{ idx, gen int }

// paneClosedMsg signals a role's process exited; gen guards against a restarted role's stale stream.
type paneClosedMsg struct{ idx, gen int }

// tickMsg drives the activity clock so working/idle and "Xs ago" stay fresh.
type tickMsg struct{}

// ipcMsg carries a control command from the unix socket into the update loop.
type ipcMsg struct{ cmd ipc.Command }

// gatewayReadyMsg reports the outcome of starting/attaching the sphragis gateway.
type gatewayReadyMsg struct {
	sup *sphragis.Supervisor
	err error
}

// gatewayHealthMsg carries a periodic gateway health probe result.
type gatewayHealthMsg struct{ up bool }

type entry struct {
	role       config.Role
	pane       *pane.Pane
	exited     bool
	booted     bool
	gen        int // bumped on restart; stale stream messages are dropped
	startedAt  time.Time
	lastActive time.Time
}

// Model drives the orchestration deck: sidebar cards plus the tiled role panes.
type Model struct {
	cfg        config.Config
	prog       *tea.Program
	panes      []*entry
	active     int
	manual     bool // user drove focus (ctrl+o or any WM action); pause auto-focus
	tree       *wm.Tree
	keys       config.Keys
	prefixed   bool // prefix armed; next key runs a WM action
	sidebar    bool // status-card column visible
	autoFocus  bool // activity steals focus ([ui] auto_focus)
	helpOn     bool // help overlay visible; any key closes it
	broadcast  bool // normal-mode keys go to every live pane
	server     *ipc.Server
	socket     string
	baseURL    string // gateway base URL handed to role env; reused on restart
	gateway    *sphragis.Supervisor
	sphragisOn bool // gateway enforcement, toggled live with ctrl+g
	gatewayUp  bool // last known gateway health (refreshed off the UI thread)
	closed     bool // closeAll ran; makes cleanup idempotent
	scrollOff  int  // focused-pane scrollback offset (0 = live tail)
	maxScroll  int  // last render's max scrollback offset, for clamping keys
	events     *slog.Logger
	eventsC    io.Closer
	w, h       int
	err        error
}

// discardLog is the fallback so dispatch never nil-panics before the event log is wired.
var discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func (m *Model) log() *slog.Logger {
	if m.events == nil {
		return discardLog
	}
	return m.events
}

// Run opens the deck for cfg and blocks until the user quits.
func Run(cfg config.Config) error {
	m := &Model{cfg: cfg}
	m.prog = tea.NewProgram(m, tea.WithAltScreen(), tea.WithoutSignalHandler())
	defer m.closeAll() // also cleans up when prog.Run returns
	// Escape hatch: even a wedged update loop exits cleanly on SIGINT/SIGTERM; Kill restores the terminal without the loop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		m.prog.Kill()
	}()
	if _, err := m.prog.Run(); err != nil {
		return err
	}
	return m.err
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tea.SetWindowTitle("choragos"), tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if m.panes == nil {
			cmd, err := m.startAll()
			if err != nil {
				m.err = err
				return m, tea.Quit
			}
			return m, cmd
		}
		m.resizePanes()
	case tea.KeyMsg:
		return m.handleKey(msg)
	case frameMsg:
		if msg.idx >= 0 && msg.idx < len(m.panes) && m.panes[msg.idx].gen == msg.gen {
			m.panes[msg.idx].lastActive = time.Now()
			if m.autoFocus && !m.manual {
				m.focusRole(msg.idx) // auto-focus whoever is producing output
			}
		}
	case paneClosedMsg:
		if msg.idx >= 0 && msg.idx < len(m.panes) && m.panes[msg.idx].gen == msg.gen {
			m.panes[msg.idx].exited = true
			m.log().Warn("pane exited", "role", m.panes[msg.idx].role.Name)
		}
	case ipcMsg:
		m.dispatch(msg.cmd)
	case gatewayReadyMsg:
		if msg.err == nil {
			m.gateway = msg.sup
			m.gatewayUp = true
			m.log().Info("gateway ready", "addr", m.cfg.Sphragis.Addr)
		} else {
			m.log().Error("gateway start failed", "err", msg.err)
		}
	case gatewayHealthMsg:
		if msg.up != m.gatewayUp {
			m.log().Warn("gateway health changed", "up", msg.up)
		}
		m.gatewayUp = msg.up
	case tickMsg:
		m.bootPanes()
		if m.sphragisOn {
			return m, tea.Batch(tick(), checkHealth(m.cfg.Sphragis.Addr))
		}
		return m, tick()
	}
	return m, nil
}

// handleKey routes a key: direct chords first, then resize mode, prefix mode, and PTY forwarding.
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlQ:
		m.closeAll()
		return m, tea.Quit
	case tea.KeyCtrlO:
		if len(m.panes) > 0 {
			m.manual = true
			m.focusRole((m.active + 1) % len(m.panes))
		}
		return m, nil
	case tea.KeyCtrlG:
		m.sphragisOn = !m.sphragisOn
		return m, nil
	case tea.KeyPgUp:
		m.scrollOff += scrollStep
		return m, nil
	case tea.KeyPgDown:
		if m.scrollOff -= scrollStep; m.scrollOff < 0 {
			m.scrollOff = 0
		}
		return m, nil
	}
	if m.helpOn {
		m.helpOn = false
		return m, nil
	}
	if m.tree != nil && m.tree.Resizing() {
		m.resizeKey(msg.String())
		return m, nil
	}
	if m.prefixed {
		m.prefixed = false
		m.wmAction(msg.String())
		return m, nil
	}
	if m.tree != nil && msg.String() == m.keys.Prefix {
		m.prefixed = true
		return m, nil
	}
	if m.broadcast {
		for _, e := range m.panes {
			if !e.exited {
				_ = e.pane.Input(keyBytes(msg))
			}
		}
		return m, nil
	}
	if e := m.current(); e != nil && !e.exited {
		_ = e.pane.Input(keyBytes(msg))
	}
	return m, nil
}

// resizeKey adjusts the focused split's ratio live; any unmapped key exits resize mode.
func (m *Model) resizeKey(key string) {
	key, reps := collapseRepeat(key) // key repeat can coalesce into one "hhh" rune msg
	var vert bool
	var delta float64
	switch key {
	case "h", "left":
		vert, delta = true, -resizeStep
	case "l", "right":
		vert, delta = true, resizeStep
	case "k", "up":
		vert, delta = false, -resizeStep
	case "j", "down":
		vert, delta = false, resizeStep
	default:
		m.tree.SetResizing(false)
		return
	}
	if m.tree.AdjustRatio(vert, delta*float64(reps)) {
		m.resizePanes()
	}
}

// collapseRepeat folds a coalesced run of one rune ("hhh") into the rune and its count.
func collapseRepeat(key string) (string, int) {
	r := []rune(key)
	if len(r) < 2 {
		return key, 1
	}
	for _, c := range r {
		if c != r[0] {
			return key, 1
		}
	}
	return string(r[0]), len(r)
}

// wmAction runs the prefix-mode action bound to key; unmapped keys are a no-op.
func (m *Model) wmAction(key string) {
	switch key {
	case m.keys.SplitVertical:
		m.split(true)
	case m.keys.SplitHorizontal:
		m.split(false)
	case m.keys.ClosePane:
		if m.tree.Close() {
			m.manual = true
			m.syncFocus()
		}
	case m.keys.FocusLeft:
		m.focusDir(wm.Left)
	case m.keys.FocusDown:
		m.focusDir(wm.Down)
	case m.keys.FocusUp:
		m.focusDir(wm.Up)
	case m.keys.FocusRight:
		m.focusDir(wm.Right)
	case m.keys.CycleNext:
		m.manual = true
		m.tree.CycleNext()
		m.syncFocus()
	case m.keys.CyclePrev:
		m.manual = true
		m.tree.CyclePrev()
		m.syncFocus()
	case m.keys.Zoom:
		m.manual = true
		m.tree.ToggleZoom()
		m.resizePanes()
	case m.keys.ResizeMode:
		m.tree.SetResizing(true)
	case m.keys.ToggleSidebar:
		m.sidebar = !m.sidebar
		m.resizePanes()
	case m.keys.Help:
		m.helpOn = true
	case m.keys.RestartRole:
		m.restartRole()
	case m.keys.Broadcast:
		m.broadcast = !m.broadcast
	}
}

// split tiles the next hidden role next to the focused tile; no-op when all roles are visible.
func (m *Model) split(vert bool) {
	role := m.nextHiddenRole()
	if role < 0 {
		return
	}
	m.manual = true
	m.tree.Split(vert, role)
	m.syncFocus()
}

// nextHiddenRole picks the first role after the focused one that has no tile.
func (m *Model) nextHiddenRole() int {
	vis := make(map[int]bool)
	for _, r := range m.tree.VisibleRoles() {
		vis[r] = true
	}
	for off := 1; off <= len(m.panes); off++ {
		i := (m.active + off) % len(m.panes)
		if !vis[i] {
			return i
		}
	}
	return -1
}

// focusDir moves focus to the geometrically adjacent tile.
func (m *Model) focusDir(d wm.Dir) {
	m.manual = true
	_, mainW, contentH := m.dims()
	if m.tree.FocusDir(d, mainW, contentH) {
		m.syncFocus()
	}
}

// focusRole shows role i: focuses its tile when visible, else retargets the focused tile.
func (m *Model) focusRole(i int) {
	if i == m.active || i < 0 || i >= len(m.panes) {
		return
	}
	if m.tree != nil {
		m.tree.Focus(i)
		m.syncFocus()
		return
	}
	m.scrollOff, m.maxScroll = 0, 0
	m.active = i
}

// syncFocus aligns active with the tree's focused tile and resizes visible panes.
func (m *Model) syncFocus() {
	if r := m.tree.FocusedRole(); r != m.active {
		m.scrollOff, m.maxScroll = 0, 0
		m.active = r
	}
	m.resizePanes()
}

// checkHealth probes the gateway off the UI thread so View never blocks on I/O.
func checkHealth(addr string) tea.Cmd {
	return func() tea.Msg { return gatewayHealthMsg{up: sphragis.Healthy(addr)} }
}

// ensureGateway starts or attaches the gateway off the UI thread.
func ensureGateway(cfg config.Sphragis) tea.Cmd {
	return func() tea.Msg {
		sup, err := sphragis.Ensure(cfg)
		return gatewayReadyMsg{sup: sup, err: err}
	}
}

// dispatch routes a command to its pane: delegate to a worker, work-done to the orchestrator, via kernel-buffered PTY writes.
func (m *Model) dispatch(cmd ipc.Command) {
	if m.gatewayBlocked() {
		m.log().Warn("dispatch refused: gateway down", "cmd", cmd.Cmd, "to", strings.Join(cmd.To, ","))
		return // fail closed: no gateway, no orchestration
	}
	switch cmd.Cmd {
	case "delegate":
		for _, name := range cmd.To {
			if e, i := m.findRole(name); e != nil && !e.exited {
				file := "worker-task-" + sanitize(name) + ".md"
				line := writeContext(file, prompt.WorkerTask(e.role, cmd.Task),
					"Read "+filepath.Join(contextDir, file)+" for your task.")
				m.log().Info("delegate", "from", "orchestrator", "to", name, "task", singleLine(cmd.Task))
				injectLine(e, line)
				if m.autoFocus && !m.manual {
					m.focusRole(i)
				}
			} else {
				m.log().Warn("delegate target unavailable", "to", name)
			}
		}
	case "work-done":
		i := m.startIdx()
		if i >= 0 && i < len(m.panes) && !m.panes[i].exited {
			m.log().Info("work-done", "to", m.panes[i].role.Name, "done", cmd.Done, "task", singleLine(cmd.Task))
			injectLine(m.panes[i], "A worker reports: "+singleLine(cmd.Task))
			if m.autoFocus && !m.manual {
				m.focusRole(i)
			}
		}
	}
}

// bootPanes injects each role's boot prompt once its pane has settled (agent ready).
func (m *Model) bootPanes() {
	now := time.Now()
	for _, e := range m.panes {
		if e.booted || e.exited {
			continue
		}
		if now.Sub(e.startedAt) < bootMinWait || now.Sub(e.lastActive) < bootSettle {
			continue
		}
		m.log().Info("boot", "role", e.role.Name, "start", e.role.Start)
		m.injectBoot(e)
		e.booted = true
	}
}

func (m *Model) injectBoot(e *entry) {
	if e.role.Start {
		file := "orchestrator-context.md"
		line := writeContext(file, prompt.OrchestratorContext(m.cfg),
			"Read "+filepath.Join(contextDir, file)+" for your role, available agents, and the delegation protocol. Acknowledge your role and wait for instructions.")
		injectLine(e, line)
		return
	}
	file := sanitize(e.role.Name) + "-brief.md"
	line := writeContext(file, prompt.WorkerBrief(e.role),
		"Read "+filepath.Join(contextDir, file)+" for your role, then stay idle until a task is delegated to you.")
	injectLine(e, line)
}

// writeContext writes content to contextDir/name; on failure it returns a diagnostic to inject instead of pointing the agent at a missing file.
func writeContext(name, content, oneLiner string) string {
	path := filepath.Join(contextDir, name)
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		return singleLine("[choragos] could not create " + contextDir + ": " + err.Error())
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return singleLine("[choragos] could not write " + path + ": " + err.Error())
	}
	return oneLiner
}

// injectLine types one line into a pane, then submits Enter separately so Claude's TUI does not swallow it as a paste.
func injectLine(e *entry, line string) {
	if err := e.pane.Input([]byte(line)); err != nil {
		e.exited = true
		return
	}
	p := e.pane
	go func() {
		time.Sleep(injectEnterDelay)
		_ = p.Input([]byte("\r"))
	}()
}

// sanitize makes a role name safe for a filename.
func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "role"
	}
	return b.String()
}

// singleLine collapses newlines so a summary submits as one PTY line.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// chromeMarkers are substrings that mark an agent TUI's persistent footer/statusline, not real activity.
var chromeMarkers = []string{"for agents", "for shortcuts", "lazy:full", "release-notes", "auto-update"}

// activityTail keeps the last n content lines, dropping TUI chrome (progress bars, statusline, hints).
func activityTail(lines []string, n int) []string {
	var kept []string
	for _, l := range lines {
		if !chromeLine(l) {
			kept = append(kept, l)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return kept
}

// chromeLine reports whether a line is agent-TUI chrome rather than meaningful output.
func chromeLine(s string) bool {
	low := strings.ToLower(s)
	for _, m := range chromeMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	glyph, total := 0, 0
	for _, r := range s {
		if r == ' ' {
			continue
		}
		total++
		if isChromeRune(r) {
			glyph++
		}
	}
	return total > 0 && glyph*2 >= total // box/block/progress glyphs dominate
}

// isChromeRune reports whether r is a box-drawing, block, geometric, or braille glyph used in TUI chrome.
func isChromeRune(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x25ff: // box drawing, block elements, geometric shapes
		return true
	case r >= 0x2800 && r <= 0x28ff: // braille (spinners/bars)
		return true
	}
	return false
}

// truncate caps a plain string to max runes, marking elision with an ellipsis.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return string(r[:1])
	}
	return string(r[:max-1]) + "…"
}

func (m *Model) findRole(name string) (*entry, int) {
	for i, e := range m.panes {
		if e.role.Name == name {
			return e, i
		}
	}
	return nil, -1
}

func (m *Model) startIdx() int {
	for i, e := range m.panes {
		if e.role.Start {
			return i
		}
	}
	return 0
}

func (m *Model) View() string {
	if m.err != nil {
		return "error: " + m.err.Error() + "\n"
	}
	if len(m.panes) == 0 {
		return "starting deck...\n"
	}
	now := time.Now()
	leftW, mainW, contentH := m.dims()

	st := make([]roleState, len(m.panes))
	for i, e := range m.panes {
		st[i] = computeStatus(e, now)
	}

	body := m.tree.Render(mainW, contentH, func(role, w, h int) string {
		return m.renderTile(role, w, h, st)
	})
	if m.helpOn {
		body = m.renderHelp(mainW, contentH)
	}
	main := lipgloss.NewStyle().Width(mainW).Height(contentH).MaxHeight(contentH).Render(body)
	if leftW > 0 {
		left := m.renderCards(leftW, contentH, st)
		main = lipgloss.JoinHorizontal(lipgloss.Top, left, main)
	}
	return main + "\n" + m.renderStats(st)
}

// renderHelp draws the keymap overlay in place of the tiled area; any key closes it.
func (m *Model) renderHelp(w, h int) string {
	k := m.keys
	rows := [][2]string{
		{"ctrl+q", "quit (graceful)"},
		{"ctrl+g", "toggle sphragis gateway"},
		{"ctrl+o", "cycle focus across roles"},
		{"PgUp/PgDn", "scrollback on the focused tile"},
		{k.Prefix + " " + k.SplitVertical, "split left/right"},
		{k.Prefix + " " + k.SplitHorizontal, "split top/bottom"},
		{k.Prefix + " " + k.ClosePane, "close tile (agent keeps running)"},
		{k.Prefix + " " + k.FocusLeft + "/" + k.FocusDown + "/" + k.FocusUp + "/" + k.FocusRight, "focus left/down/up/right"},
		{k.Prefix + " " + k.CycleNext + " / " + k.CyclePrev, "cycle tiles next/prev"},
		{k.Prefix + " " + k.Zoom, "zoom focused tile"},
		{k.Prefix + " " + k.ResizeMode, "resize mode (h/j/k/l, other key exits)"},
		{k.Prefix + " " + k.ToggleSidebar, "toggle sidebar"},
		{k.Prefix + " " + k.RestartRole, "restart focused role"},
		{k.Prefix + " " + k.Broadcast, "toggle broadcast input"},
		{k.Prefix + " " + k.TaskBoard, "task board"},
		{k.Prefix + " " + k.Search, "search scrollback"},
		{k.Prefix + " " + k.Help, "this help"},
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(accentColor).Render("keybindings") + "\n\n")
	for _, r := range rows {
		b.WriteString(lipgloss.NewStyle().Foreground(accentColor).Width(16).Render(r[0]))
		b.WriteString(" " + r[1] + "\n")
	}
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("press any key to close"))
	if w < 6 || h < 5 {
		return truncate(b.String(), w*h)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Width(w - 2).Height(h - 2).MaxHeight(h).
		Render(b.String())
}

// dims returns the sidebar width (0 when hidden), main-area width, and content height.
func (m *Model) dims() (leftW, mainW, contentH int) {
	contentH = m.h - 1
	if contentH < 1 {
		contentH = 1
	}
	mainW = m.w
	if !m.sidebar {
		if mainW < 1 {
			mainW = 1
		}
		return 0, mainW, contentH
	}
	leftW = m.w / 3
	if leftW < minSidebar {
		leftW = minSidebar
	}
	if leftW > m.w-1 {
		leftW = m.w - 1
	}
	if leftW < 1 {
		leftW = 1
	}
	mainW = m.w - leftW
	if mainW < 1 {
		mainW = 1
	}
	return leftW, mainW, contentH
}

// tileContent maps a tile's outer dims to its pane content area; chrome is border plus header.
func tileContent(w, h int) (cw, ch int, chrome bool) {
	if w < 6 || h < 5 {
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
		return w, h, false
	}
	return w - 2, h - 3, true
}

// renderTile draws one role pane as a w x h tile: header + live screen (or scrollback) in a status-colored border.
func (m *Model) renderTile(role, w, h int, st []roleState) string {
	e := m.panes[role]
	focused := role == m.active
	cw, ch, chrome := tileContent(w, h)
	content := e.pane.Render()
	scrolled := false
	if focused && m.scrollOff > 0 {
		var maxOff int
		content, maxOff = e.pane.Scrollback(cw, ch, m.scrollOff)
		m.maxScroll = maxOff
		if m.scrollOff > maxOff {
			m.scrollOff = maxOff
		}
		scrolled = m.scrollOff > 0
	}
	if !chrome {
		return lipgloss.NewStyle().Width(w).Height(h).MaxWidth(w).MaxHeight(h).Render(content)
	}
	border := dimColor
	switch {
	case scrolled:
		border = scrollColor
	case focused:
		border = accentColor
	case st[role].waiting:
		border = waitingColor
	}
	nameStyle := lipgloss.NewStyle().Bold(true)
	if focused {
		nameStyle = nameStyle.Foreground(accentColor)
	}
	header := lipgloss.NewStyle().MaxWidth(cw).Render(
		lipgloss.NewStyle().Foreground(st[role].color).Render(st[role].dot) + " " +
			nameStyle.Render(e.role.Name) + "  " +
			lipgloss.NewStyle().Faint(true).Render(st[role].label))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Width(w - 2).Height(h - 2).MaxHeight(h).
		Render(header + "\n" + content)
}

// renderCards is the left column: one status card per role, with a tail activity preview.
func (m *Model) renderCards(width int, height int, st []roleState) string {
	var cards []string
	for i, e := range m.panes {
		border := dimColor
		nameStyle := lipgloss.NewStyle().Bold(true)
		if i == m.active {
			border = accentColor
			nameStyle = nameStyle.Foreground(accentColor)
		}
		if st[i].waiting {
			border = waitingColor
		}
		inner := nameStyle.Render(fmt.Sprintf("%d %s", i+1, e.role.Name)) + "\n" +
			lipgloss.NewStyle().Foreground(st[i].color).Render(st[i].dot) + " " +
			lipgloss.NewStyle().Faint(true).Render(st[i].label)
		for _, l := range activityTail(e.pane.TailLines(40), cardActivityLines) {
			inner += "\n" + lipgloss.NewStyle().Faint(true).Render(truncate(singleLine(l), width-4))
		}
		card := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Width(width - 2).
			Render(inner)
		cards = append(cards, card)
	}
	col := lipgloss.JoinVertical(lipgloss.Left, cards...)
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(col)
}

func (m *Model) renderStats(st []roleState) string {
	active, working, waiting := 0, 0, 0
	for _, s := range st {
		if s.exited {
			continue
		}
		active++
		switch {
		case s.waiting:
			waiting++
		case s.working:
			working++
		}
	}
	scroll := ""
	if m.scrollOff > 0 {
		scroll = lipgloss.NewStyle().Foreground(scrollColor).Render(fmt.Sprintf(" · scrollback ↑%d", m.scrollOff))
	}
	txt := fmt.Sprintf("%d active · %d working · %d waiting · %s · %s wm · ctrl+g gateway · ctrl+o focus · ctrl+q quit",
		active, working, waiting, m.gatewayLabel(), m.keys.Prefix)
	return m.modeLabel() + lipgloss.NewStyle().Faint(true).Render(txt) + scroll
}

// modeLabel is the status-line WM mode indicator: prefix armed, resize mode, zoom, or broadcast.
func (m *Model) modeLabel() string {
	var out string
	if m.broadcast {
		out += lipgloss.NewStyle().Foreground(waitingColor).Bold(true).Render("[BCAST] ")
	}
	switch {
	case m.tree != nil && m.tree.Resizing():
		out += lipgloss.NewStyle().Foreground(waitingColor).Bold(true).Render("[RESIZE h/j/k/l] ")
	case m.prefixed:
		out += lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("[PREFIX] ")
	case m.tree != nil && m.tree.Zoomed():
		out += lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("[ZOOM] ")
	}
	return out
}

func (m *Model) gatewayLabel() string {
	if !m.sphragisOn {
		return lipgloss.NewStyle().Foreground(dimColor).Render("sphragis off")
	}
	if m.gatewayUp {
		return lipgloss.NewStyle().Foreground(workingColor).Render("sphragis ●")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("sphragis down")
}

// roleState is one pane's status, computed once per frame and shared across the columns.
type roleState struct {
	dot     string
	color   lipgloss.Color
	label   string
	working bool
	waiting bool
	exited  bool
}

// inputPrompts are visible-screen markers meaning the agent is blocked waiting for the user, not idle.
var inputPrompts = []string{
	"esc to cancel",
	"do you want to proceed",
	"allow access",
	"(y/n)",
	"[y/n]",
	"press enter to continue",
}

// computeStatus classifies a pane: exited, waiting for input, working, or idle.
func computeStatus(e *entry, now time.Time) roleState {
	switch {
	case e.exited:
		return roleState{dot: "○", color: dimColor, label: "exited", exited: true}
	case needsInput(e):
		return roleState{dot: "◆", color: waitingColor, label: "waiting for input", waiting: true}
	case now.Sub(e.lastActive) < workingWindow:
		return roleState{dot: "●", color: workingColor, label: "working", working: true}
	default:
		return roleState{dot: "◦", color: idleColor, label: "idle " + humanizeSince(now.Sub(e.lastActive))}
	}
}

// needsInput reports whether the pane's visible screen shows a blocking prompt.
func needsInput(e *entry) bool {
	if e.exited || e.pane == nil {
		return false
	}
	return promptInLines(e.pane.TailLines(14))
}

// promptInLines reports whether any line carries a known blocking-prompt marker.
func promptInLines(lines []string) bool {
	for _, l := range lines {
		low := strings.ToLower(l)
		for _, marker := range inputPrompts {
			if strings.Contains(low, marker) {
				return true
			}
		}
	}
	return false
}

func humanizeSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// resizePanes sizes every visible tile's pane; hidden panes keep their last size.
func (m *Model) resizePanes() {
	if m.tree == nil {
		return
	}
	_, mainW, contentH := m.dims()
	for _, tile := range m.tree.Layout(mainW, contentH) {
		if tile.Role < 0 || tile.Role >= len(m.panes) {
			continue
		}
		e := m.panes[tile.Role]
		if e.exited {
			continue
		}
		cw, ch, _ := tileContent(tile.W, tile.H)
		_ = e.pane.Resize(cw, ch) // pane clamps non-positive dims
	}
}

func (m *Model) startAll() (tea.Cmd, error) {
	m.events, m.eventsC = newEventLog()
	m.socket = ipc.SocketPath()
	srv, err := ipc.Serve(m.socket, func(c ipc.Command) { m.prog.Send(ipcMsg{cmd: c}) })
	if err != nil {
		return nil, fmt.Errorf("ipc serve: %w", err)
	}
	m.server = srv
	m.log().Info("deck starting", "roles", len(m.cfg.Roles), "sphragis", m.cfg.Sphragis.IsEnabled())

	m.sphragisOn = m.cfg.Sphragis.IsEnabled()
	m.keys = m.cfg.Keys.Defaulted()
	m.autoFocus = m.cfg.UI.IsAutoFocus()
	m.sidebar = m.cfg.UI.SidebarStart()
	m.baseURL = ""
	if m.sphragisOn {
		m.baseURL = m.cfg.Sphragis.BaseURL()
	}

	// the deck opens as a single tile showing the start role
	_, mainW, contentH := m.dims()
	cw, ch, _ := tileContent(mainW, contentH)
	panes, err := startPanes(m.cfg, cw, ch, m.socket, m.baseURL)
	if err != nil {
		return nil, err
	}
	m.panes = panes
	now := time.Now()
	for i, e := range panes {
		e.startedAt = now
		e.lastActive = now
		m.watchPane(e, i)
	}
	for i, e := range panes {
		if e.role.Start {
			m.active = i
			break
		}
	}
	m.tree = wm.New(m.active)
	if m.sphragisOn {
		return ensureGateway(m.cfg.Sphragis), nil
	}
	return nil, nil
}

// send forwards a message into the UI loop; nil-safe for tests without a running program.
func (m *Model) send(msg tea.Msg) {
	if m.prog != nil {
		m.prog.Send(msg)
	}
}

// watchPane streams a pane's output into the UI loop until it exits; gen drops stale streams after a restart.
func (m *Model) watchPane(e *entry, idx int) {
	gen := e.gen
	p := e.pane
	go func() {
		_ = p.Stream(func() { m.send(frameMsg{idx: idx, gen: gen}) })
		m.send(paneClosedMsg{idx: idx, gen: gen})
	}()
}

// restartRole respawns the focused tile's role, killing the old process if still alive.
func (m *Model) restartRole() {
	e := m.current()
	if e == nil {
		return
	}
	_ = e.pane.Close() // idempotent; unblocks the old stream so its exit is dropped by gen
	cw, ch := m.focusedTileContent()
	p, err := startRole(e.role, cw, ch, roleEnv(m.socket, m.baseURL))
	if err != nil {
		e.exited = true
		m.log().Error("restart failed", "role", e.role.Name, "err", err)
		return
	}
	e.pane = p
	e.gen++
	e.exited = false
	e.booted = false
	e.startedAt = time.Now()
	e.lastActive = time.Now()
	m.log().Info("role restarted", "role", e.role.Name)
	m.watchPane(e, m.active)
}

// focusedTileContent returns the focused tile's pane content dims.
func (m *Model) focusedTileContent() (int, int) {
	_, mainW, contentH := m.dims()
	for _, t := range m.tree.Layout(mainW, contentH) {
		if t.Focused {
			cw, ch, _ := tileContent(t.W, t.H)
			return cw, ch
		}
	}
	return 80, 24
}

// gatewayBlocked reports whether fail-closed enforcement should refuse dispatch (on, fail-closed, and down).
func (m *Model) gatewayBlocked() bool {
	return m.sphragisOn && m.cfg.Sphragis.IsFailClosed() && !m.gatewayUp
}

// roleEnv builds the child env wiring the control socket and (when set) the gateway.
func roleEnv(socket, baseURL string) []string {
	env := append(os.Environ(), ipc.EnvSocket+"="+socket)
	if baseURL != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+baseURL)
	}
	return env
}

// startRole spawns one role's PTY pane with its log sink.
func startRole(r config.Role, cols, rows int, env []string) (*pane.Pane, error) {
	cmd := exec.Command(r.Command, roleArgs(r)...)
	cmd.Env = env
	p, err := pane.Start(cmd, cols, rows)
	if err != nil {
		return nil, fmt.Errorf("start role %q: %w", r.Name, err)
	}
	if f := openLog(r.Name); f != nil {
		p.SetLog(f)
	}
	return p, nil
}

// startPanes spawns one PTY pane per role, wiring the control socket and (when on) the gateway via env.
func startPanes(cfg config.Config, cols, rows int, socket, baseURL string) ([]*entry, error) {
	env := roleEnv(socket, baseURL)
	var entries []*entry
	for _, r := range cfg.Roles {
		p, err := startRole(r, cols, rows, env)
		if err != nil {
			for _, e := range entries {
				_ = e.pane.Close()
			}
			return nil, err
		}
		entries = append(entries, &entry{role: r, pane: p})
	}
	return entries, nil
}

// openLog opens a per-role output log under contextDir/logs; logging is best-effort so failures are silent.
func openLog(role string) *os.File {
	dir := filepath.Join(contextDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	f, err := os.Create(filepath.Join(dir, sanitize(role)+".log"))
	if err != nil {
		return nil
	}
	return f
}

// newEventLog opens the control-plane event log (delegate/work-done/boot/lifecycle); on failure it discards.
func newEventLog() (*slog.Logger, io.Closer) {
	dir := filepath.Join(contextDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return discardLog, nil
	}
	f, err := os.Create(filepath.Join(dir, "events.log"))
	if err != nil {
		return discardLog, nil
	}
	return slog.New(slog.NewTextHandler(f, nil)), f
}

func roleArgs(r config.Role) []string {
	args := append([]string{}, r.Args...)
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	return args
}

func (m *Model) current() *entry {
	if m.active < 0 || m.active >= len(m.panes) {
		return nil
	}
	return m.panes[m.active]
}

func (m *Model) closeAll() {
	if m.closed {
		return
	}
	m.closed = true
	m.log().Info("deck stopping")
	if m.server != nil {
		_ = m.server.Close()
		_ = os.Remove(m.socket)
	}
	// SIGTERM every agent first so they all run their SessionEnd hooks in parallel, then force after the shared deadline.
	deadline := time.Now().Add(gracefulTimeout)
	for _, e := range m.panes {
		e.pane.Terminate()
	}
	for _, e := range m.panes {
		e.pane.Shutdown(deadline)
	}
	_ = m.gateway.Close()
	if m.eventsC != nil {
		_ = m.eventsC.Close()
	}
}

// keyBytes maps a key event to the bytes forwarded to the PTY (minimal set).
func keyBytes(k tea.KeyMsg) []byte {
	switch k.Type {
	case tea.KeyRunes, tea.KeySpace:
		return []byte(string(k.Runes))
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	default:
		return []byte(k.String())
	}
}
