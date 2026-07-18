// SPDX-License-Identifier: Apache-2.0

// Package deck is the Bubble Tea TUI: a toggleable status-card sidebar plus a tiling window manager over the role panes.
package deck

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sphragis-oss/choragos/internal/checkpoint"
	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/sphragis"
	"github.com/sphragis-oss/choragos/internal/wire"
	"github.com/sphragis-oss/choragos/internal/wm"
)

const (
	minSidebar        = 24
	cardActivityLines = 3  // tail rows previewed per status card
	cardTailRows      = 40 // rows scanned for card previews and prompt probes
	scrollStep        = 5  // rows moved per PgUp/PgDn
	workingWindow     = 2 * time.Second
)

// deckTheme holds the resolved status colors: [ui.theme] overrides over the classic palette.
type deckTheme struct {
	accent  lipgloss.Color // focused: cyan
	working lipgloss.Color // working: green
	waiting lipgloss.Color // blocked on user input: orange
	scroll  lipgloss.Color // scrollback active: magenta
	idle    lipgloss.Color // idle: grey
	dim     lipgloss.Color // exited/unfocused
}

// themeFrom applies non-empty [ui.theme] values over the defaults; Load already validated them.
func themeFrom(t config.Theme) deckTheme {
	th := deckTheme{accent: "45", working: "42", waiting: "214", scroll: "213", idle: "244", dim: "240"}
	for _, o := range []struct {
		dst *lipgloss.Color
		v   string
	}{{&th.accent, t.Accent}, {&th.working, t.Working}, {&th.waiting, t.Waiting}, {&th.scroll, t.Scroll}, {&th.idle, t.Idle}, {&th.dim, t.Dim}} {
		if o.v != "" {
			*o.dst = lipgloss.Color(o.v)
		}
	}
	return th
}

// resizeStep is the ratio delta per keypress in resize mode.
const resizeStep = 0.05

// tickMsg drives the activity clock so working/idle and "Xs ago" stay fresh.
type tickMsg struct{}

// gatewayReadyMsg reports the outcome of starting/attaching the sphragis gateway.
type gatewayReadyMsg struct {
	sup *sphragis.Supervisor
	err error
}

// gatewayHealthMsg carries a periodic gateway health probe result.
type gatewayHealthMsg struct{ up bool }

// editorDoneMsg reports the editor spawned from the approval overlay exiting.
type editorDoneMsg struct{ err error }

// cardHit is one sidebar card's y-extent in the last render, mapping clicks to its role.
type cardHit struct{ role, top, bot int }

// Model drives the orchestration deck: the UI half. It embeds the session core
// and adds focus, layout, overlays, and rendering on top.
type Model struct {
	*session
	prog       *tea.Program
	active     int
	manual     bool // user drove focus (ctrl+o or any WM action); pause auto-focus
	tree       *wm.Tree
	keys       config.Keys
	prefixed   bool // prefix armed; next key runs a WM action
	sidebar    bool // status-card column visible
	autoFocus  bool // delegations and input prompts steal focus ([ui] auto_focus)
	helpOn     bool // help overlay visible; any key closes it
	boardOn    bool // task board overlay visible; j/k/v navigate, any other key closes
	boardSel   int  // task board selection, index into board
	pagerOn    bool // pager overlay visible; scrolls briefs/reports in-app
	pagerTitle string
	pagerLines []string
	pagerOff   int               // first visible pager line
	rbOn       bool              // rollback confirm overlay visible
	rbStore    *checkpoint.Store // pending rollback; nil once applied or when rbMsg reports
	rbTarget   checkpoint.Entry
	rbExtra    []string   // files the rollback will delete
	rbFiles    int        // files the rollback will restore
	rbMsg      string     // error or result text; any key closes
	rbWarn     string     // unresolved-task caution shown in the overlay
	broadcast  bool       // normal-mode keys go to every live pane
	searching  bool       // typing a scrollback search query
	searchBuf  string     // query being typed
	searchQ    string     // committed query; n/N navigate while scrolled
	usage      usageMsg   // last per-role token snapshot from the gateway metrics
	scrollOff  int        // focused-pane scrollback offset (0 = live tail)
	maxScroll  int        // last render's max scrollback offset, for clamping keys
	cardHits   []cardHit  // sidebar card y-extents from the last render, for click focus
	th         deckTheme  // resolved status colors ([ui.theme] over the defaults)
	remote     *wire.Conn // attached to a detached session; core actions go over the wire
	w, h       int
	err        error
}

// wireSession points the core's UI callbacks at this Model.
func (m *Model) wireSession() {
	m.th = themeFrom(m.cfg.UI.Theme)
	m.focusFn = func(i int) {
		if m.autoFocus && !m.manual {
			m.focusRole(i)
		}
	}
}

// programOptions builds the Bubble Tea options; [ui] mouse=false skips capture to restore terminal-native selection.
func programOptions(cfg config.Config) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithoutSignalHandler()}
	if cfg.UI.IsMouse() {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	return opts
}

// Run opens the deck for cfg and blocks until the user quits.
func Run(cfg config.Config) (err error) {
	m := &Model{session: &session{cfg: cfg}}
	m.notify = func(v any) {
		if m.prog != nil {
			m.prog.Send(v)
		}
	}
	m.wireSession()
	// bubbletea restores the terminal before re-panicking; we stop the agents and keep the stack
	defer func() {
		if r := recover(); r != nil {
			m.closeAll()
			err = fmt.Errorf("choragos crashed: %v (details in %s)", r, writeCrashLog(r))
		}
	}()
	m.prog = tea.NewProgram(m, programOptions(cfg)...)
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
	return tea.Batch(tea.SetWindowTitle(windowTitle()), tick())
}

// windowTitle names the terminal tab after the workspace; attach runs in the session's directory too.
func windowTitle() string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return "choragos"
	}
	return "choragos · " + filepath.Base(wd)
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
	case tea.MouseMsg:
		m.handleMouse(msg)
	case frameMsg:
		if msg.idx >= 0 && msg.idx < len(m.panes) && m.panes[msg.idx].gen == msg.gen {
			m.panes[msg.idx].lastActive = time.Now()
		}
	case paneClosedMsg:
		if msg.idx >= 0 && msg.idx < len(m.panes) && m.panes[msg.idx].gen == msg.gen {
			m.panes[msg.idx].exited = true
			m.log().Warn("pane exited", "role", m.panes[msg.idx].role.Name)
			m.autoRestart(m.panes[msg.idx], msg.idx)
		}
	case ipcMsg:
		if msg.cmd.Cmd == "shutdown" {
			m.closeAll()
			return m, tea.Quit
		}
		m.applyCommand(msg.cmd)
	case remoteEvMsg:
		m.applyRemoteEvent(msg.ev)
	case connLostMsg:
		m.err = fmt.Errorf("session connection lost: %w", msg.err)
		return m, tea.Quit
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
	case editorDoneMsg:
		if msg.err != nil {
			m.log().Error("editor failed", "err", msg.err)
		}
	case usageMsg:
		m.usage = msg
	case tickMsg:
		if m.remote != nil {
			// the server boots, probes, and rings; the client only refreshes usage
			if m.sphragisOn && m.gatewayUp {
				return m, tea.Batch(tick(), fetchUsage(m.cfg.Sphragis.Addr, m.cfg.Pricing))
			}
			return m, tick()
		}
		m.bootPanes()
		m.checkWaiting()
		m.checkTimeouts()
		m.maybeLogTokens()
		if m.sphragisOn {
			cmds := []tea.Cmd{tick(), checkHealth(m.cfg.Sphragis.Addr)}
			if m.gatewayUp {
				cmds = append(cmds, fetchUsage(m.cfg.Sphragis.Addr, m.cfg.Pricing))
			}
			return m, tea.Batch(cmds...)
		}
		return m, tick()
	}
	return m, nil
}

// handleKey routes a key: direct chords first, then resize mode, prefix mode, and PTY forwarding.
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlQ:
		if m.remote != nil {
			_ = m.remote.WriteEvent(wire.Event{Kind: "quit"}) // stops the detached session too
			return m, tea.Quit
		}
		m.closeAll()
		return m, tea.Quit
	case tea.KeyCtrlO:
		if len(m.panes) > 0 {
			m.manual = true
			m.focusRole((m.active + 1) % len(m.panes))
		}
		return m, nil
	case tea.KeyCtrlG:
		m.sphragisOn = !m.sphragisOn // remote: optimistic, the status event resyncs
		if m.remote != nil {
			_ = m.remote.WriteEvent(wire.Event{Kind: "sphragis"})
		}
		return m, nil
	case tea.KeyPgUp:
		if m.pagerOn {
			m.pagerOff -= m.pagerPage()
			return m, nil
		}
		m.scrollOff += scrollStep
		return m, nil
	case tea.KeyPgDown:
		if m.pagerOn {
			m.pagerOff += m.pagerPage()
			return m, nil
		}
		if m.scrollOff -= scrollStep; m.scrollOff < 0 {
			m.scrollOff = 0
		}
		return m, nil
	}
	if m.pagerOn {
		m.pagerKey(msg)
		return m, nil
	}
	if m.rbOn {
		m.rollbackKey(msg)
		return m, nil
	}
	if m.boardOn {
		open, cmd := m.boardKey(msg)
		if !open {
			m.boardOn = false
		}
		return m, cmd
	}
	if m.helpOn {
		m.helpOn = false
		return m, nil
	}
	if len(m.gates) > 0 {
		return m, m.gateKey(msg) // modal: the pipeline is paused until the user decides
	}
	if m.searching {
		m.searchKey(msg)
		return m, nil
	}
	if m.tree != nil && m.tree.Resizing() {
		m.resizeKey(msg.String())
		return m, nil
	}
	if m.prefixed {
		m.prefixed = false
		if msg.String() == m.keys.Detach && m.remote != nil {
			_ = m.remote.WriteEvent(wire.Event{Kind: "detach"}) // the session keeps running
			return m, tea.Quit
		}
		m.wmAction(msg.String())
		if m.remote != nil && m.tree != nil {
			_ = m.remote.WriteEvent(wire.Event{Kind: "layout", Data: m.tree.Marshal()}) // checkpoint for the next attach
		}
		return m, nil
	}
	if m.tree != nil && msg.String() == m.keys.Prefix {
		m.prefixed = true
		return m, nil
	}
	if m.scrollOff > 0 && m.searchQ != "" && msg.Type == tea.KeyRunes {
		switch string(msg.Runes) {
		case "n":
			m.searchJump(1)
			return m, nil
		case "N":
			m.searchJump(-1)
			return m, nil
		}
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

// handleMouse focuses the clicked or hovered tile and drives scrollback with the wheel.
func (m *Model) handleMouse(msg tea.MouseMsg) {
	if m.tree == nil {
		return
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp && msg.Action == tea.MouseActionPress:
		m.focusTileAt(msg.X, msg.Y)
		m.scrollOff += scrollStep
	case msg.Button == tea.MouseButtonWheelDown && msg.Action == tea.MouseActionPress:
		m.focusTileAt(msg.X, msg.Y)
		if m.scrollOff -= scrollStep; m.scrollOff < 0 {
			m.scrollOff = 0
		}
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		leftW, _, contentH := m.dims()
		if msg.Y >= contentH {
			return // the status row is not clickable
		}
		if msg.X-leftW < 0 {
			for _, c := range m.cardHits {
				if msg.Y >= c.top && msg.Y < c.bot {
					m.surfaceRole(c.role)
					return
				}
			}
			return
		}
		m.focusTileAt(msg.X, msg.Y)
	}
}

// focusTileAt focuses the tile under the pointer; sidebar and status row are left alone.
func (m *Model) focusTileAt(x, y int) {
	leftW, mainW, contentH := m.dims()
	tx := x - leftW
	if tx < 0 || y >= contentH {
		return
	}
	for _, t := range m.tree.Layout(mainW, contentH) {
		if tx >= t.X && tx < t.X+t.W && y >= t.Y && y < t.Y+t.H {
			if t.Role != m.active {
				m.manual = true
				m.focusRole(t.Role)
			}
			return
		}
	}
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
		if m.remote != nil {
			_ = m.remote.WriteEvent(wire.Event{Kind: "restart", Idx: m.active})
			break
		}
		m.restartRole()
	case m.keys.PauseRole:
		if m.remote != nil {
			_ = m.remote.WriteEvent(wire.Event{Kind: "pause", Idx: m.active})
			break
		}
		m.togglePause(m.active)
	case m.keys.Reload:
		if m.remote != nil {
			_ = m.remote.WriteEvent(wire.Event{Kind: "reload"})
			break
		}
		m.reloadConfig()
	case m.keys.Broadcast:
		m.broadcast = !m.broadcast
	case m.keys.TaskBoard:
		m.boardOn = true
		m.boardSel = len(m.board) - 1
	case m.keys.Search:
		m.searching = true
		m.searchBuf = ""
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			m.surfaceRole(int(key[0] - '1')) // card numbers are 1-based
		}
	}
}

// searchKey edits the query; Enter jumps to the nearest match above, Esc cancels.
func (m *Model) searchKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEnter:
		m.searching = false
		m.searchQ = m.searchBuf
		m.searchJump(1)
	case tea.KeyEsc:
		m.searching = false
		m.searchBuf = ""
	case tea.KeyBackspace:
		if r := []rune(m.searchBuf); len(r) > 0 {
			m.searchBuf = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		m.searchBuf += " "
	case tea.KeyRunes:
		m.searchBuf += string(msg.Runes)
	}
}

// searchJump scrolls the focused pane to the nearest match: dir>0 older (up), dir<0 newer (down).
func (m *Model) searchJump(dir int) {
	e := m.current()
	if e == nil || m.searchQ == "" {
		return
	}
	cw, ch := m.focusedTileContent()
	lines := e.pane.HistoryLines(cw)
	total := len(lines)
	if total == 0 || ch < 1 {
		return
	}
	cur := total - m.scrollOff - ch // top row of the current view
	q := strings.ToLower(m.searchQ)
	match := -1
	if dir > 0 {
		for r := min(cur-1, total-1); r >= 0; r-- {
			if strings.Contains(strings.ToLower(lines[r]), q) {
				match = r
				break
			}
		}
	} else {
		for r := cur + 1; r < total; r++ {
			if strings.Contains(strings.ToLower(lines[r]), q) {
				match = r
				break
			}
		}
	}
	if match < 0 {
		return
	}
	if off := total - match - ch; off > 0 {
		m.scrollOff = off // renderTile clamps to the pane's max offset
	} else {
		m.scrollOff = 0
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

// nextHiddenRole picks the first role after the focused one that has no tile; tombstoned roles are skipped.
func (m *Model) nextHiddenRole() int {
	vis := make(map[int]bool)
	for _, r := range m.tree.VisibleRoles() {
		vis[r] = true
	}
	for off := 1; off <= len(m.panes); off++ {
		i := (m.active + off) % len(m.panes)
		if !vis[i] && !m.panes[i].gone {
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
// surfaceRole focuses role i on user demand, retargeting the focused tile when it is hidden; gone roles are ignored.
func (m *Model) surfaceRole(i int) {
	if i < 0 || i >= len(m.panes) || m.panes[i].gone {
		return
	}
	m.manual = true
	m.focusRole(i)
}

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

// applyCommand routes a socket command: reload converges the roster (with UI cleanup),
// everything else goes to the core dispatcher.
func (m *Model) applyCommand(cmd ipc.Command) {
	if cmd.Cmd == "reload" {
		m.reloadConfig() // config convergence, not orchestration: allowed even with the gateway down
		return
	}
	m.dispatch(cmd)
}

// reloadConfig converges the roster on the config file and applies the UI effects:
// tombstoned roles lose their tiles, and the layout resizes when anything changed.
func (m *Model) reloadConfig() {
	cw, ch := m.focusedTileContent()
	retired, changed := m.reload(cw, ch)
	for _, idx := range retired {
		if m.tree != nil && m.tree.FocusRole(idx) {
			if !m.tree.Close() {
				m.tree.Focus(m.startIdx()) // last tile: retarget to the start role
			}
			m.syncFocus()
		}
	}
	if changed {
		m.resizePanes()
	}
}

// boardKey handles task-board navigation; open=false means the key closes the board.
func (m *Model) boardKey(msg tea.KeyMsg) (open bool, cmd tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.boardSel < len(m.board)-1 {
			m.boardSel++
		}
		return true, nil
	case "k", "up":
		if m.boardSel > 0 {
			m.boardSel--
		}
		return true, nil
	case "v", "V":
		if m.boardSel < len(m.board) {
			if f := m.board[m.boardSel].file; f != "" {
				return true, m.viewFile(filepath.Base(f), f)
			}
		}
		return true, nil
	case "e", "E":
		if m.boardSel < len(m.board) {
			if f := m.board[m.boardSel].file; f != "" {
				return true, editBrief(f)
			}
		}
		return true, nil
	case "u", "U":
		m.startRollback()
		return true, nil
	}
	return false, nil
}

// gateKey resolves the oldest pending gate: y approves and injects, n rejects back to the orchestrator, e edits the brief.
func (m *Model) gateKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type != tea.KeyRunes || len(msg.Runes) == 0 {
		return nil
	}
	g := m.gates[0]
	switch string(msg.Runes) {
	case "y", "Y":
		if m.remote != nil {
			m.gates = m.gates[1:] // optimistic; the server's gates event resyncs
			_ = m.remote.WriteEvent(wire.Event{Kind: "gate", Approve: true})
			return nil
		}
		m.approveGate()
	case "n", "N":
		if m.remote != nil {
			m.gates = m.gates[1:]
			_ = m.remote.WriteEvent(wire.Event{Kind: "gate"})
			return nil
		}
		m.rejectGate()
	case "e", "E":
		if g.cmd.Brief == "" || g.reason != "" {
			return nil // nothing to edit; the gate stays
		}
		m.log().Info("gate brief edit", "to", g.to, "brief", g.cmd.Brief)
		return editBrief(g.cmd.Brief)
	case "v", "V":
		if g.reason != "" && g.report != "" {
			return m.viewFile("report: "+filepath.Base(g.report), g.report) // the gate stays
		}
		if g.cmd.Brief != "" {
			return m.viewFile("brief: "+filepath.Base(g.cmd.Brief), g.cmd.Brief) // the gate stays
		}
	}
	return nil
}

// editBrief suspends the deck and opens the file in $VISUAL/$EDITOR (fallback vi); overlays stay pending.
func editBrief(path string) tea.Cmd {
	c := exec.Command("sh", "-c", resolveEditor("vi")+" "+shellQuote(path))
	return tea.ExecProcess(c, func(err error) tea.Msg { return editorDoneMsg{err: err} })
}

// resolveEditor returns $VISUAL/$EDITOR, or fallback when both are unset.
func resolveEditor(fallback string) string {
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return fallback
}

// viewFile opens a brief/report per [ui] viewer: the user's editor, or the in-app pager (also the no-editor fallback).
func (m *Model) viewFile(title, path string) tea.Cmd {
	if m.cfg.UI.IsEditorViewer() && resolveEditor("") != "" {
		return editBrief(path)
	}
	m.openPager(title, path)
	return nil
}

// shellQuote single-quotes s for sh -c.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// chromeMarkers are substrings that mark an agent TUI's persistent footer/statusline, not real activity.
var chromeMarkers = []string{"for agents", "for shortcuts", "lazy:full", "release-notes", "auto-update"}

// activityTail keeps the last n content lines, dropping TUI chrome (progress bars, statusline, hints).
func activityTail(lines []string, n int, extra []string) []string {
	var kept []string
	for _, l := range lines {
		if !chromeLine(l, extra) {
			kept = append(kept, l)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return kept
}

// chromeLine reports whether a line is agent-TUI chrome rather than meaningful output.
func chromeLine(s string, extra []string) bool {
	low := strings.ToLower(s)
	for _, m := range chromeMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	for _, m := range extra {
		if m != "" && strings.Contains(low, strings.ToLower(m)) {
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
		st[i] = computeStatus(e, now, m.th)
	}

	body := m.tree.Render(mainW, contentH, func(role, w, h int) string {
		return m.renderTile(role, w, h, st)
	})
	if m.boardOn {
		body = m.renderBoard(mainW, contentH)
	}
	if m.helpOn {
		body = m.renderHelp(mainW, contentH)
	}
	if len(m.gates) > 0 {
		body = m.renderGate(mainW, contentH) // approval outranks the other overlays
	}
	if m.rbOn {
		body = m.renderRollback(mainW, contentH) // a started rollback outranks a gate; one keypress resolves it
	}
	if m.pagerOn {
		body = m.renderPager(mainW, contentH) // reading outranks even the gate; esc returns to it
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
		{k.Prefix + " " + k.PauseRole, "pause/resume focused role (SIGSTOP)"},
		{k.Prefix + " " + k.Reload, "reload config (add/remove roles)"},
		{k.Prefix + " " + k.Detach, "detach (attached sessions; agents keep running)"},
		{k.Prefix + " " + k.Broadcast, "toggle broadcast input"},
		{k.Prefix + " " + k.TaskBoard, "task board"},
		{k.Prefix + " 1..9", "focus role by card number"},
		{k.Prefix + " " + k.Search, "search scrollback"},
		{k.Prefix + " " + k.Help, "this help"},
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(m.th.accent).Render("keybindings") + "\n\n")
	for _, r := range rows {
		b.WriteString(lipgloss.NewStyle().Foreground(m.th.accent).Width(16).Render(r[0]))
		b.WriteString(" " + r[1] + "\n")
	}
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("press any key to close"))
	if w < 6 || h < 5 {
		return truncate(b.String(), w*h)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(m.th.accent).
		Width(w - 2).Height(h - 2).MaxHeight(h).
		Render(b.String())
}

// renderGate draws the approval prompt for the oldest pending delegation; y approves, n rejects.
func (m *Model) renderGate(w, h int) string {
	g := m.gates[0]
	label := singleLine(g.cmd.Task)
	if label == "" && g.cmd.Brief != "" {
		label = "brief: " + filepath.Base(g.cmd.Brief)
	}
	var b strings.Builder
	title := "delegation awaiting approval"
	if g.reason != "" {
		title = "judge loop needs a decision"
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(m.th.waiting).Render(title) + "\n\n")
	row := func(k, v string) {
		b.WriteString(lipgloss.NewStyle().Foreground(m.th.accent).Width(10).Render(k) + " " + v + "\n")
	}
	row("to", g.to)
	row("task", truncate(label, w-14))
	if g.reason != "" {
		row("reason", truncate(g.reason, w-14))
		if g.report != "" {
			row("report", truncate(g.report, w-14))
		}
	}
	if g.cmd.Brief != "" {
		row("brief", truncate(g.cmd.Brief, w-14))
	}
	row("queued", g.at.Format("15:04:05"))
	if n := len(m.gates) - 1; n > 0 {
		b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("+%d more waiting behind this one", n)) + "\n")
	}
	keys, hint := "[y] approve   [n] reject", "to amend it, edit the brief file first, then approve"
	switch {
	case g.reason != "":
		keys = "[y] accept the result   [n] reject"
		hint = "the autonomous loop stopped; accept hands the last result to the orchestrator"
		if g.report != "" {
			keys = "[y] accept the result   [v] view report   [n] reject"
		}
	case g.cmd.Brief != "":
		keys = "[y] approve   [v] view brief   [e] edit brief   [n] reject"
		hint = "v pages the brief in-app, e opens your $EDITOR; the gate stays until y or n"
		if m.cfg.UI.IsEditorViewer() {
			hint = "v and e open the brief in your $EDITOR; the gate stays until y or n"
		}
	}
	b.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render(keys) + "\n")
	b.WriteString(lipgloss.NewStyle().Faint(true).Render(hint))
	if w < 6 || h < 5 {
		return truncate(b.String(), w*h)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(m.th.waiting).
		Width(w - 2).Height(h - 2).MaxHeight(h).
		Render(b.String())
}

// renderBoard draws the delegation-event board in place of the tiled area; j/k select, v views, any other key closes.
func (m *Model) renderBoard(w, h int) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(m.th.accent).Render("task board") + "\n\n")
	if len(m.board) == 0 {
		b.WriteString(lipgloss.NewStyle().Faint(true).Render("no delegations yet"))
	}
	if m.boardSel >= len(m.board) {
		m.boardSel = len(m.board) - 1
	}
	if m.boardSel < 0 {
		m.boardSel = 0
	}
	rows, start := m.board, 0
	if maxRows := h - 6; maxRows > 0 && len(rows) > maxRows {
		if start = len(rows) - maxRows; m.boardSel < start {
			start = m.boardSel // the window follows the selection upward
		}
		rows = rows[start : start+maxRows]
	}
	for i, ev := range rows {
		kind := ev.kind
		status := ""
		switch {
		case ev.kind == "delegate" && ev.doneAt.IsZero() && ev.timedOut:
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(" timeout")
		case ev.kind == "delegate" && ev.doneAt.IsZero():
			status = lipgloss.NewStyle().Foreground(m.th.waiting).Render(" pending")
		case ev.kind == "delegate":
			status = lipgloss.NewStyle().Foreground(m.th.working).Render(" ✓ " + ev.doneAt.Sub(ev.at).Round(time.Second).String())
		case ev.kind == "work-done" && ev.done:
			kind = "work-done ✓"
		}
		if ev.round > 0 {
			kind += fmt.Sprintf(" r%d", ev.round)
		}
		if ev.score != "" {
			status += lipgloss.NewStyle().Foreground(m.th.scroll).Render(" " + ev.score)
		}
		id := ""
		if ev.id != "" {
			id = ev.id + " "
		}
		task := ev.task
		if ev.file != "" {
			task += "  [" + filepath.Base(ev.file) + "]"
		}
		marker := "  "
		if start+i == m.boardSel {
			marker = lipgloss.NewStyle().Foreground(m.th.accent).Render("▸ ")
		}
		line := marker + ev.at.Format("15:04:05") + "  " + id +
			lipgloss.NewStyle().Foreground(m.th.accent).Render(kind) + " → " + ev.to + status + "  " +
			lipgloss.NewStyle().Faint(true).Render(truncate(task, w-50))
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("j/k select · v view brief/report · e open in $EDITOR · u roll back workspace · any other key closes"))
	if w < 6 || h < 5 {
		return truncate(b.String(), w*h)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(m.th.accent).
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
	var content string
	scrolled := false
	if focused && m.scrollOff > 0 {
		var maxOff int
		content, maxOff = e.pane.Scrollback(cw, ch, m.scrollOff)
		m.maxScroll = maxOff
		if m.scrollOff > maxOff {
			m.scrollOff = maxOff
		}
		scrolled = m.scrollOff > 0
	} else if focused {
		content = e.renderCachedCursor() // live tail shows the child's cursor
	} else {
		content = e.renderCached() // idle panes serve the cache; no per-frame grid walk
	}
	if !chrome {
		return lipgloss.NewStyle().Width(w).Height(h).MaxWidth(w).MaxHeight(h).Render(content)
	}
	border := m.th.dim
	switch {
	case scrolled:
		border = m.th.scroll
	case focused:
		border = m.th.accent
	case st[role].waiting:
		border = m.th.waiting
	}
	nameStyle := lipgloss.NewStyle().Bold(true)
	if focused {
		nameStyle = nameStyle.Foreground(m.th.accent)
	}
	header := lipgloss.NewStyle().MaxWidth(cw).Render(
		lipgloss.NewStyle().Foreground(st[role].color).Render(st[role].dot) + " " +
			nameStyle.Render(e.role.Name) + "  " +
			lipgloss.NewStyle().Faint(true).Render(st[role].label))
	tile := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Width(w - 2).Height(h - 2).MaxHeight(h).
		Render(header + "\n" + content)
	if scrolled {
		tile = overlayScrollThumb(tile, ch, m.scrollOff, m.maxScroll)
	}
	return tile
}

// overlayScrollThumb swaps the right border of the view's proportional window for a block thumb.
func overlayScrollThumb(tile string, ch, off, maxOff int) string {
	total := maxOff + ch // rows of scrollable history
	if total < 1 || ch < 1 {
		return tile
	}
	tlen := ch * ch / total
	if tlen < 1 {
		tlen = 1
	}
	tstart := (maxOff - off) * ch / total
	lines := strings.Split(tile, "\n")
	for i := 0; i < tlen; i++ {
		n := 2 + tstart + i // border row, header row, then content
		if n >= len(lines)-1 {
			break
		}
		if j := strings.LastIndex(lines[n], "│"); j >= 0 {
			lines[n] = lines[n][:j] + "█" + lines[n][j+len("│"):]
		}
	}
	return strings.Join(lines, "\n")
}

// renderCards is the left column: one status card per role, with a tail activity preview.
func (m *Model) renderCards(width int, height int, st []roleState) string {
	var cards []string
	m.cardHits = m.cardHits[:0]
	y := 0
	for i, e := range m.panes {
		if e.gone {
			continue
		}
		border := m.th.dim
		nameStyle := lipgloss.NewStyle().Bold(true)
		if i == m.active {
			border = m.th.accent
			nameStyle = nameStyle.Foreground(m.th.accent)
		}
		if st[i].waiting {
			border = m.th.waiting
		}
		inner := nameStyle.Render(fmt.Sprintf("%d %s", i+1, e.role.Name)) + "\n" +
			lipgloss.NewStyle().Foreground(st[i].color).Render(st[i].dot) + " " +
			lipgloss.NewStyle().Faint(true).Render(st[i].label)
		if md := m.roleModel(e.role); md != "" {
			inner += "\n" + lipgloss.NewStyle().Faint(true).Render(truncate(md, width-4))
		}
		if u := m.usage[e.role.Name].usageLabel(); u != "" {
			inner += "\n" + lipgloss.NewStyle().Faint(true).Render(truncate(u, width-4))
		}
		for _, l := range activityTail(e.tailCached(), cardActivityLines, e.role.ChromeMarkers) {
			inner += "\n" + lipgloss.NewStyle().Faint(true).Render(truncate(singleLine(l), width-4))
		}
		card := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Width(width - 2).
			Render(inner)
		hgt := lipgloss.Height(card)
		m.cardHits = append(m.cardHits, cardHit{role: i, top: y, bot: y + hgt})
		y += hgt
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
		scroll = lipgloss.NewStyle().Foreground(m.th.scroll).Render(fmt.Sprintf(" · scrollback ↑%d/%d", m.scrollOff, m.maxScroll)) +
			lipgloss.NewStyle().Faint(true).Render(" · PgDn live · "+m.keys.Prefix+" "+m.keys.Search+" search")
	}
	gated := ""
	if len(m.gates) > 0 {
		gated = lipgloss.NewStyle().Foreground(m.th.waiting).Bold(true).Render(fmt.Sprintf(" · %d awaiting approval", len(m.gates)))
	}
	txt := fmt.Sprintf("%d active · %d working · %d waiting · %s · %s wm · ctrl+g gateway · ctrl+o focus · ctrl+q quit",
		active, working, waiting, m.gatewayLabel(), m.keys.Prefix)
	return m.modeLabel() + lipgloss.NewStyle().Faint(true).Render(txt) + gated + scroll
}

// modeLabel is the status-line WM mode indicator: prefix armed, resize mode, zoom, or broadcast.
func (m *Model) modeLabel() string {
	var out string
	if m.broadcast {
		out += lipgloss.NewStyle().Foreground(m.th.waiting).Bold(true).Render("[BCAST] ")
	}
	if m.searching {
		out += lipgloss.NewStyle().Foreground(m.th.scroll).Bold(true).Render("[SEARCH /" + m.searchBuf + "] ")
	}
	switch {
	case m.tree != nil && m.tree.Resizing():
		out += lipgloss.NewStyle().Foreground(m.th.waiting).Bold(true).Render("[RESIZE h/j/k/l] ")
	case m.prefixed:
		out += lipgloss.NewStyle().Foreground(m.th.accent).Bold(true).Render("[PREFIX] ")
	case m.tree != nil && m.tree.Zoomed():
		out += lipgloss.NewStyle().Foreground(m.th.accent).Bold(true).Render("[ZOOM] ")
	}
	return out
}

func (m *Model) gatewayLabel() string {
	if !m.sphragisOn {
		return lipgloss.NewStyle().Foreground(m.th.dim).Render("sphragis off")
	}
	if m.gatewayUp {
		return lipgloss.NewStyle().Foreground(m.th.working).Render("sphragis ●")
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

// computeStatus classifies a pane: exited, waiting for input, working, or idle.
func computeStatus(e *entry, now time.Time, th deckTheme) roleState {
	switch {
	case e.exited:
		return roleState{dot: "○", color: th.dim, label: "exited", exited: true}
	case e.paused:
		return roleState{dot: "❚❚", color: th.waiting, label: "paused"}
	case needsInput(e):
		return roleState{dot: "◆", color: th.waiting, label: "waiting for input", waiting: true}
	case now.Sub(e.lastActive) < workingWindow:
		return roleState{dot: "●", color: th.working, label: "working", working: true}
	default:
		return roleState{dot: "◦", color: th.idle, label: "idle " + humanizeSince(now.Sub(e.lastActive))}
	}
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
		_ = e.pane.Resize(cw, ch) // pane clamps non-positive dims; bumps Seq for the caches
	}
}

// startAll wires the UI to config, starts the session core, and opens as a single tile on the start role.
func (m *Model) startAll() (tea.Cmd, error) {
	m.keys = m.cfg.Keys.Defaulted()
	m.autoFocus = m.cfg.UI.IsAutoFocus()
	m.sidebar = m.cfg.UI.SidebarStart()
	if m.cfg.UI.IsBell() {
		m.bellFn = func() { _, _ = os.Stdout.WriteString("\a") }
	}
	_, mainW, contentH := m.dims()
	cw, ch, _ := tileContent(mainW, contentH)
	if err := m.start(cw, ch); err != nil {
		return nil, err
	}
	m.active = m.startIdx()
	m.tree = wm.New(m.active)
	if m.sphragisOn {
		return ensureGateway(m.cfg.Sphragis), nil
	}
	return nil, nil
}

// restartRole respawns the focused tile's role, killing the old process if still alive.
func (m *Model) restartRole() {
	e := m.current()
	if e == nil {
		return
	}
	cw, ch := m.focusedTileContent()
	m.restart(e, m.active, cw, ch)
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

func (m *Model) current() *entry {
	if m.active < 0 || m.active >= len(m.panes) {
		return nil
	}
	return m.panes[m.active]
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
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	default:
		// control KeyTypes equal their ASCII byte (ctrl+a = 0x01); anything else is dropped,
		// never typed into the pane as its key name
		if k.Type >= 0 && k.Type < 0x20 {
			return []byte{byte(k.Type)}
		}
		return nil
	}
}
