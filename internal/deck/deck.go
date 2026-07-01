// SPDX-License-Identifier: Apache-2.0

// Package deck is the Bubble Tea TUI: status cards (33%) plus an auto-expanding accordion of role panes (67%).
package deck

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
	"github.com/sphragis-oss/choragos/internal/prompt"
	"github.com/sphragis-oss/choragos/internal/sphragis"
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

// frameMsg signals a pane produced new output; idx marks which pane.
type frameMsg struct{ idx int }

// paneClosedMsg signals a role's process exited.
type paneClosedMsg struct{ idx int }

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
	startedAt  time.Time
	lastActive time.Time
}

// Model drives the two-column orchestration deck.
type Model struct {
	cfg        config.Config
	prog       *tea.Program
	panes      []*entry
	active     int
	manual     bool // user pressed ctrl+o; pause auto-expand until next real focus steal
	server     *ipc.Server
	socket     string
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
	m.prog = tea.NewProgram(m, tea.WithAltScreen())
	defer m.closeAll() // also cleans up on SIGINT/SIGTERM, when prog.Run returns
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
		switch msg.Type {
		case tea.KeyCtrlQ:
			m.closeAll()
			return m, tea.Quit
		case tea.KeyCtrlO:
			if len(m.panes) > 0 {
				m.setActive((m.active + 1) % len(m.panes))
				m.manual = true
			}
		case tea.KeyCtrlG:
			m.sphragisOn = !m.sphragisOn
		case tea.KeyPgUp:
			m.scrollOff += scrollStep
		case tea.KeyPgDown:
			if m.scrollOff -= scrollStep; m.scrollOff < 0 {
				m.scrollOff = 0
			}
		default:
			if e := m.current(); e != nil && !e.exited {
				_ = e.pane.Input(keyBytes(msg))
			}
		}
	case frameMsg:
		if msg.idx >= 0 && msg.idx < len(m.panes) {
			m.panes[msg.idx].lastActive = time.Now()
			if !m.manual {
				m.setActive(msg.idx) // auto-expand whoever is producing output
			}
		}
	case paneClosedMsg:
		if msg.idx >= 0 && msg.idx < len(m.panes) {
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
				if !m.manual {
					m.setActive(i)
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
			if !m.manual {
				m.setActive(i)
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
	d := computeLayout(len(m.panes), m.w, m.h)
	contentH := m.h - 1

	st := make([]roleState, len(m.panes))
	for i, e := range m.panes {
		st[i] = computeStatus(e, now)
	}

	left := m.renderCards(d.leftW, contentH, st)
	right := lipgloss.NewStyle().Width(d.rightW).Height(contentH).MaxHeight(contentH).
		Render(m.renderAccordion(d, st))
	main := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return main + "\n" + m.renderStats(st)
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

// renderAccordion is the right column: collapsed headers plus the expanded pane (live or scrolled back).
func (m *Model) renderAccordion(d layoutDims, st []roleState) string {
	var b strings.Builder
	for i, e := range m.panes {
		focused := i == m.active
		b.WriteString(m.paneHeader(i, focused, d.rightW, st[i]))
		b.WriteByte('\n')
		if focused && d.paneH > 0 {
			border := accentColor
			content := e.pane.Render()
			if m.scrollOff > 0 {
				var maxOff int
				content, maxOff = e.pane.Scrollback(d.paneW, d.paneH, m.scrollOff)
				m.maxScroll = maxOff
				if m.scrollOff > maxOff {
					m.scrollOff = maxOff
				}
				border = scrollColor
			}
			box := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(border).
				Width(d.paneW).Height(d.paneH).
				Render(content)
			b.WriteString(box)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// paneHeader renders one role's status row in the accordion.
func (m *Model) paneHeader(i int, focused bool, width int, st roleState) string {
	e := m.panes[i]
	caret := "  "
	nameStyle := lipgloss.NewStyle()
	if focused {
		caret = lipgloss.NewStyle().Foreground(accentColor).Render("▸") + " "
		nameStyle = nameStyle.Bold(true).Foreground(accentColor)
	}
	line := caret +
		lipgloss.NewStyle().Foreground(st.color).Render(st.dot) + " " +
		nameStyle.Render(e.role.Name) + "  " +
		lipgloss.NewStyle().Faint(true).Render(st.label)
	return lipgloss.NewStyle().Width(width).Render(line)
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
	txt := fmt.Sprintf("%d active · %d working · %d waiting · %s · PgUp/PgDn scroll · ctrl+g gateway · ctrl+o focus · ctrl+q quit",
		active, working, waiting, m.gatewayLabel())
	return lipgloss.NewStyle().Faint(true).Render(txt) + scroll
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

// layoutDims holds the column widths and the focused pane's content size.
type layoutDims struct{ leftW, rightW, paneW, paneH int }

// computeLayout splits width 33/67 (cards / accordion) and sizes the focused pane to fill the right column.
func computeLayout(n, width, height int) layoutDims {
	leftW := width / 3
	if leftW < minSidebar {
		leftW = minSidebar
	}
	if leftW > width-1 {
		leftW = width - 1
	}
	if leftW < 1 {
		leftW = 1
	}
	rightW := width - leftW
	if rightW < 1 {
		rightW = 1
	}
	paneW := rightW - 2
	if paneW < 1 {
		paneW = 1
	}
	paneH := height - n - 3 // status bar + every header row + pane border; may be <= 0 on tiny terminals, box is skipped then
	return layoutDims{leftW: leftW, rightW: rightW, paneW: paneW, paneH: paneH}
}

func (m *Model) resizePanes() {
	d := computeLayout(len(m.panes), m.w, m.h)
	for _, e := range m.panes {
		if !e.exited {
			_ = e.pane.Resize(d.paneW, d.paneH) // pane clamps non-positive dims
		}
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
	baseURL := ""
	if m.sphragisOn {
		baseURL = m.cfg.Sphragis.BaseURL()
	}

	d := computeLayout(len(m.cfg.Roles), m.w, m.h)
	panes, err := startPanes(m.cfg, d.paneW, d.paneH, m.socket, baseURL)
	if err != nil {
		return nil, err
	}
	m.panes = panes
	now := time.Now()
	for i, e := range panes {
		e.startedAt = now
		e.lastActive = now
		go func() {
			_ = e.pane.Stream(func() { m.prog.Send(frameMsg{idx: i}) })
			m.prog.Send(paneClosedMsg{idx: i})
		}()
	}
	for i, e := range panes {
		if e.role.Start {
			m.active = i
			break
		}
	}
	if m.sphragisOn {
		return ensureGateway(m.cfg.Sphragis), nil
	}
	return nil, nil
}

// gatewayBlocked reports whether fail-closed enforcement should refuse dispatch (on, fail-closed, and down).
func (m *Model) gatewayBlocked() bool {
	return m.sphragisOn && m.cfg.Sphragis.IsFailClosed() && !m.gatewayUp
}

// startPanes spawns one PTY pane per role, wiring the control socket and (when on) the gateway via env.
func startPanes(cfg config.Config, cols, rows int, socket, baseURL string) ([]*entry, error) {
	env := append(os.Environ(), ipc.EnvSocket+"="+socket)
	if baseURL != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+baseURL)
	}
	var entries []*entry
	for _, r := range cfg.Roles {
		cmd := exec.Command(r.Command, roleArgs(r)...)
		cmd.Env = env
		p, err := pane.Start(cmd, cols, rows)
		if err != nil {
			for _, e := range entries {
				_ = e.pane.Close()
			}
			return nil, fmt.Errorf("start role %q: %w", r.Name, err)
		}
		if f := openLog(r.Name); f != nil {
			p.SetLog(f)
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

// setActive focuses pane i, dropping any scrollback since it belonged to the previous pane.
func (m *Model) setActive(i int) {
	if i != m.active {
		m.scrollOff = 0
		m.maxScroll = 0
	}
	m.active = i
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
