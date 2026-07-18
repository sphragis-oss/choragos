// SPDX-License-Identifier: Apache-2.0

// The session core: roster, PTYs, tasks, gates, IPC, and supervision, with no
// UI dependency (no bubbletea/lipgloss/wm imports here). The Model embeds it
// and talks to it through notify (core -> UI messages) and focusFn (focus
// requests), so a socket transport can replace both without touching the core.
package deck

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/sphragis-oss/choragos/internal/checkpoint"
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
	// injection is verified on screen; re-typed after bootVerifyAfter, at most bootMaxTries times
	bootVerifyAfter = 3 * time.Second
	bootMaxTries    = 2
)

// injectEnterDelay separates the typed text from its submit; Claude's TUI treats text+Enter in one write as a paste.
const injectEnterDelay = 200 * time.Millisecond

// gracefulTimeout is how long a quit waits for agents to exit on SIGTERM (running SessionEnd hooks) before force-killing.
const gracefulTimeout = 5 * time.Second

// frameMsg signals a pane produced new output; gen guards against a restarted role's stale stream.
type frameMsg struct{ idx, gen int }

// paneClosedMsg signals a role's process exited; gen guards against a restarted role's stale stream.
type paneClosedMsg struct{ idx, gen int }

// ipcMsg carries a control command from the unix socket into the update loop.
type ipcMsg struct{ cmd ipc.Command }

type entry struct {
	role       config.Role
	pane       *pane.Pane
	exited     bool
	gone       bool // tombstoned by a config reload; the index stays valid and is never reused
	booted     bool
	waiting    bool // last observed waiting-for-input state, for bell edge detection
	gen        int  // bumped on restart; stale stream messages are dropped
	startedAt  time.Time
	lastActive time.Time
	bootLine   string // injected one-liner, kept for on-screen verification
	bootSentAt time.Time
	bootTries  int
	bootOK     bool
	restarts   int  // auto-restarts consumed; reset by a manual restart
	paused     bool // process group SIGSTOPped via prefix+p; resume credits timeouts
	pausedAt   time.Time
	// screen caches keyed by the pane's write sequence, so idle panes cost nothing per frame
	renderSeq   uint64
	renderPane  *pane.Pane
	cacheRender string
	curSeq      uint64
	curPane     *pane.Pane
	cacheCur    string
	tailSeq     uint64
	tailPane    *pane.Pane
	cacheTail   []string
}

// renderCached returns the pane's live screen, re-rendering only after the screen changed.
func (e *entry) renderCached() string {
	if s := e.pane.Seq(); e.renderPane != e.pane || e.renderSeq != s || e.cacheRender == "" {
		e.cacheRender = e.pane.Render()
		e.renderPane, e.renderSeq = e.pane, s
	}
	return e.cacheRender
}

// renderCachedCursor is renderCached with the cursor cell shown; only the focused tile uses it.
func (e *entry) renderCachedCursor() string {
	if s := e.pane.Seq(); e.curPane != e.pane || e.curSeq != s || e.cacheCur == "" {
		e.cacheCur = e.pane.RenderCursor()
		e.curPane, e.curSeq = e.pane, s
	}
	return e.cacheCur
}

// tailCached returns the pane's recent non-blank rows, re-scanning only after the screen changed.
func (e *entry) tailCached() []string {
	if s := e.pane.Seq(); e.tailPane != e.pane || e.tailSeq != s {
		e.cacheTail = e.pane.TailLines(cardTailRows)
		e.tailPane, e.tailSeq = e.pane, s
	}
	return e.cacheTail
}

// lastN returns the up-to-n most recent entries of lines.
func lastN(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

// session is the deck's core: everything that must survive a future UI detach.
type session struct {
	cfg        config.Config
	panes      []*entry
	board      []taskEvent
	gates      []pendingGate         // delegations awaiting user approval, oldest first
	loops      map[string]*judgeLoop // judge loops keyed by their in-flight task id
	taskSeq    int                   // task id counter for delegations
	server     *ipc.Server
	socket     string
	baseURL    string // gateway base URL handed to role env; reused on restart
	gateway    *sphragis.Supervisor
	sphragisOn bool              // gateway enforcement, toggled live with ctrl+g
	gatewayUp  bool              // last known gateway health (refreshed off the UI thread)
	lastTokens time.Time         // last event-log token snapshot, paced by tokenSnapInterval
	closed     bool              // closeAll ran; makes cleanup idempotent
	ckpt       *checkpoint.Store // pre-task workspace snapshots; nil when disabled or not a git repo
	bellFn     func()            // rings the terminal bell; nil disables ([ui] bell)
	events     *slog.Logger
	eventsC    io.Closer
	notify     func(any) // core -> UI message pump; nil-safe via send
	focusFn    func(int) // UI focus request (delegations, work-done, input prompts)
}

// send forwards a core message to the UI loop; nil-safe for tests without a pump.
func (s *session) send(v any) {
	if s.notify != nil {
		s.notify(v)
	}
}

// focus asks the UI to surface role i; the UI decides whether auto-focus allows it.
func (s *session) focus(i int) {
	if s.focusFn != nil {
		s.focusFn(i)
	}
}

// pendingGate is a delegation held for user approval before it reaches the worker.
type pendingGate struct {
	cmd ipc.Command
	to  string
	at  time.Time
	// judge fallback gates: reason set means approve accepts the result, reject asks for a revise
	reason string
	report string // last report attached to a fallback gate
	loopID string
}

// taskEvent is one delegation-protocol event, shown on the task board.
type taskEvent struct {
	at       time.Time
	kind     string // delegate | work-done
	id       string
	to       string
	task     string
	file     string // brief or report path attached to the event, if any
	done     bool
	doneAt   time.Time // delegate rows: when the matching work-done arrived
	timedOut bool      // delegate rows: outlived the role's timeout before any work-done
	round    int       // judge loop round this event belongs to; 0 = unjudged
	score    string    // judge verdict ("6/10") once parsed
}

// boardCap bounds the in-memory task history.
const boardCap = 200

func (s *session) recordTask(ev taskEvent) {
	s.board = append(s.board, ev)
	if len(s.board) > boardCap {
		s.board = s.board[len(s.board)-boardCap:]
	}
}

// resolveTask marks the delegation with this id as reported.
func (s *session) resolveTask(id string) {
	if id == "" {
		return
	}
	for i := len(s.board) - 1; i >= 0; i-- {
		ev := &s.board[i]
		if ev.kind == "delegate" && ev.id == id && ev.doneAt.IsZero() {
			ev.doneAt = time.Now()
			return
		}
	}
}

// discardLog is the fallback so dispatch never nil-panics before the event log is wired.
var discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func (s *session) log() *slog.Logger {
	if s.events == nil {
		return discardLog
	}
	return s.events
}

// start opens the control socket, spawns every role at cw x ch, and begins streaming them.
func (s *session) start(cw, ch int) error {
	s.events, s.eventsC = newEventLog()
	s.socket = ipc.SocketPath()
	srv, err := ipc.Serve(s.socket, func(c ipc.Command) { s.send(ipcMsg{cmd: c}) })
	if err != nil {
		return fmt.Errorf("ipc serve: %w", err)
	}
	s.server = srv
	ipc.WriteMeta(s.socket) // sidecar for `choragos ls`
	wd, _ := os.Getwd()
	s.log().Info("deck starting", "roles", len(s.cfg.Roles), "sphragis", s.cfg.Sphragis.IsEnabled(), "dir", wd)
	for _, w := range s.cfg.Warnings {
		s.log().Warn("config", "warning", w)
	}
	s.initCheckpoints()
	s.sphragisOn = s.cfg.Sphragis.IsEnabled()
	if s.sphragisOn && sphragis.AutoOff(s.cfg.Sphragis) {
		s.sphragisOn = false
		s.log().Warn("sphragis auto-off: command not in PATH and no gateway listening; set [sphragis] enabled = true to require it",
			"command", s.cfg.Sphragis.Command, "addr", s.cfg.Sphragis.Addr)
	}
	s.baseURL = ""
	if s.sphragisOn {
		s.baseURL = s.cfg.Sphragis.BaseURL()
	}
	panes, err := startPanes(s.cfg, cw, ch, s.socket, s.baseURL)
	if err != nil {
		return err
	}
	s.panes = panes
	now := time.Now()
	for i, e := range panes {
		e.startedAt = now
		e.lastActive = now
		s.watchPane(e, i)
	}
	return nil
}

// watchPane streams a pane's output into the UI loop until it exits; gen drops stale streams after a restart.
func (s *session) watchPane(e *entry, idx int) {
	gen := e.gen
	p := e.pane
	role := e.role.Name
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.log().Error("pane stream panic", "role", role, "panic", r, "log", writeCrashLog(r))
				s.send(paneClosedMsg{idx: idx, gen: gen})
			}
		}()
		_ = p.Stream(func() { s.send(frameMsg{idx: idx, gen: gen}) })
		s.send(paneClosedMsg{idx: idx, gen: gen})
	}()
}

// dispatch routes a control command to its pane: delegate to a worker, work-done to the orchestrator.
func (s *session) dispatch(cmd ipc.Command) {
	if s.gatewayBlocked() {
		s.log().Warn("dispatch refused: gateway down", "cmd", cmd.Cmd, "to", strings.Join(cmd.To, ","))
		return // fail closed: no gateway, no orchestration
	}
	switch cmd.Cmd {
	case "delegate":
		for _, name := range cmd.To {
			e, i := s.findRole(name)
			if e == nil || e.exited {
				s.log().Warn("delegate target unavailable", "to", name)
				continue
			}
			if e.paused {
				s.log().Warn("delegate to paused role; input buffers until resume", "to", name)
			}
			if e.role.Approve {
				s.gates = append(s.gates, pendingGate{cmd: cmd, to: name, at: time.Now()})
				s.log().Info("delegate gated", "to", name, "task", singleLine(cmd.Task), "brief", cmd.Brief)
				if s.bellFn != nil {
					s.bellFn()
				}
				s.runHook(s.cfg.UI.OnGate, name, singleLine(cmd.Task))
				continue
			}
			id := s.deliverDelegate(e, i, cmd)
			s.maybeStartLoop(id, e, cmd)
		}
	case "work-done":
		if s.handleJudgedDone(cmd) {
			return // a judge loop owns this id; the orchestrator hears only loop outcomes
		}
		i := s.startIdx()
		if i >= 0 && i < len(s.panes) && !s.panes[i].exited {
			summary := singleLine(cmd.Task)
			if summary == "" {
				summary = "see report"
			}
			line := "A worker reports: " + summary
			if cmd.Report != "" {
				line += " Full report: read " + cmd.Report
			}
			s.log().Info("work-done", "id", cmd.ID, "to", s.panes[i].role.Name, "done", cmd.Done, "task", summary, "report", cmd.Report)
			s.recordTask(taskEvent{at: time.Now(), kind: "work-done", id: cmd.ID, to: s.panes[i].role.Name, task: summary, file: cmd.Report, done: cmd.Done})
			s.resolveTask(cmd.ID)
			injectLine(s.panes[i], line)
			s.focus(i)
		}
	}
}

// initCheckpoints wires the snapshot store when enabled and the directory is a git repository.
func (s *session) initCheckpoints() {
	if !s.cfg.Checkpoints.IsEnabled() {
		return
	}
	st := checkpoint.New(".")
	if ok, reason := st.Active(); !ok {
		s.log().Warn("checkpoints disabled", "reason", reason)
		return
	}
	s.ckpt = st
	if n, err := st.Prune(s.cfg.Checkpoints.KeepCount()); err != nil {
		s.log().Warn("checkpoint prune failed", "err", err)
	} else if n > 0 {
		s.log().Info("checkpoints pruned", "removed", n)
	}
}

// snapshotTask checkpoints the workspace before a task reaches its worker; failure warns, never blocks.
func (s *session) snapshotTask(id, role, label string) {
	if s.ckpt == nil {
		return
	}
	t0 := time.Now()
	name := fmt.Sprintf("%d-%s", t0.Unix(), id)
	ref, err := s.ckpt.Snapshot(name, id+" -> "+role+": "+label, "head: "+s.ckpt.Head())
	if err != nil {
		s.log().Warn("checkpoint failed", "task", id, "err", err)
		return
	}
	s.log().Info("checkpoint", "task", id, "ref", ref, "took", time.Since(t0).Round(time.Millisecond))
}

// deliverDelegate hands an (approved) delegation to a worker: task file, board entry, PTY injection.
func (s *session) deliverDelegate(e *entry, i int, cmd ipc.Command) string {
	s.taskSeq++
	id := fmt.Sprintf("T%d", s.taskSeq)
	task := cmd.Task
	if cmd.Brief != "" {
		task = strings.TrimSpace("Read " + cmd.Brief + " for the full brief.\n\n" + cmd.Task)
	}
	label := singleLine(cmd.Task)
	if label == "" {
		label = "brief: " + filepath.Base(cmd.Brief)
	}
	file := "worker-task-" + sanitize(e.role.Name) + ".md"
	line := writeContext(file, prompt.WorkerTask(e.role, task, id),
		"Read "+filepath.Join(contextDir, file)+" for your task.")
	s.log().Info("delegate", "id", id, "from", "orchestrator", "to", e.role.Name, "task", label, "brief", cmd.Brief)
	s.recordTask(taskEvent{at: time.Now(), kind: "delegate", id: id, to: e.role.Name, task: label, file: cmd.Brief})
	s.snapshotTask(id, e.role.Name, label)
	injectLine(e, line)
	s.focus(i)
	return id
}

// approveGate resolves the oldest pending gate: delivery for entry gates, acceptance for judge fallbacks.
func (s *session) approveGate() {
	if len(s.gates) == 0 {
		return
	}
	g := s.gates[0]
	s.gates = s.gates[1:]
	if g.reason != "" {
		s.resolveFallback(g, true)
		return
	}
	if e, i := s.findRole(g.to); e != nil && !e.exited {
		s.log().Info("delegate approved", "to", g.to, "waited", time.Since(g.at).Round(time.Second))
		id := s.deliverDelegate(e, i, g.cmd)
		s.maybeStartLoop(id, e, g.cmd)
	} else {
		s.log().Warn("delegate target unavailable", "to", g.to)
	}
}

// rejectGate drops the oldest pending gate and tells the orchestrator to revise.
func (s *session) rejectGate() {
	if len(s.gates) == 0 {
		return
	}
	g := s.gates[0]
	s.gates = s.gates[1:]
	if g.reason != "" {
		s.resolveFallback(g, false)
		return
	}
	s.log().Warn("delegate rejected", "to", g.to, "task", singleLine(g.cmd.Task), "brief", g.cmd.Brief)
	if i := s.startIdx(); i >= 0 && i < len(s.panes) && !s.panes[i].exited {
		injectLine(s.panes[i], "[choragos] The user rejected your delegation to "+g.to+". Revise the plan or the brief and delegate again if still needed.")
	}
}

// runHook fires a [ui] notification hook in the background; a broken hook only logs, never wedges the deck.
func (s *session) runHook(hook, role, task string) {
	if hook == "" {
		return
	}
	c := exec.Command("sh", "-c", hook)
	c.Env = append(os.Environ(), "CHORAGOS_ROLE="+role, "CHORAGOS_TASK="+task)
	logger := s.log()
	go func() {
		if err := c.Run(); err != nil {
			logger.Error("notification hook failed", "role", role, "err", err)
		}
	}()
}

// checkWaiting rings the bell once per transition into waiting-for-input.
func (s *session) checkWaiting() {
	for i, e := range s.panes {
		if e.paused {
			continue // a stopped process must not look or ring as waiting
		}
		w := needsInput(e)
		if w && !e.waiting {
			s.log().Info("waiting for input", "role", e.role.Name)
			if s.bellFn != nil {
				s.bellFn()
			}
			s.runHook(s.cfg.UI.OnInput, e.role.Name, "")
			s.focus(i) // surface whoever blocks on input
		}
		e.waiting = w
	}
}

// togglePause freezes or resumes the focused role's process group.
func (s *session) togglePause(i int) {
	if i < 0 || i >= len(s.panes) {
		return
	}
	e := s.panes[i]
	if e.exited || e.gone {
		return
	}
	if e.paused {
		if err := e.pane.Resume(); err != nil {
			s.log().Error("resume failed", "role", e.role.Name, "err", err)
			return
		}
		s.creditPause(e.role.Name, e.pausedAt)
		e.paused = false
		s.log().Info("role resumed", "role", e.role.Name, "paused", time.Since(e.pausedAt).Round(time.Second).String())
		return
	}
	if err := e.pane.Pause(); err != nil {
		s.log().Error("pause failed", "role", e.role.Name, "err", err)
		return
	}
	e.paused = true
	e.pausedAt = time.Now()
	s.log().Info("role paused", "role", e.role.Name)
}

// creditPause shifts open delegations forward so paused time never counts toward timeouts.
func (s *session) creditPause(role string, pausedAt time.Time) {
	now := time.Now()
	for i := range s.board {
		ev := &s.board[i]
		if ev.kind != "delegate" || ev.to != role || !ev.doneAt.IsZero() || ev.timedOut {
			continue
		}
		from := ev.at
		if pausedAt.After(from) {
			from = pausedAt
		}
		ev.at = ev.at.Add(now.Sub(from))
	}
}

// checkTimeouts flags delegations that outlived their role's wall-clock limit; fires once per delegation.
func (s *session) checkTimeouts() {
	now := time.Now()
	for i := range s.board {
		ev := &s.board[i]
		if ev.kind != "delegate" || ev.timedOut || !ev.doneAt.IsZero() {
			continue
		}
		e, _ := s.findRole(ev.to)
		if e == nil || e.paused {
			continue
		}
		d := e.role.TimeoutDuration()
		if d <= 0 || now.Sub(ev.at) < d {
			continue
		}
		ev.timedOut = true
		if loop, ok := s.loops[ev.id]; ok && loop.phase == "judge" {
			delete(s.loops, ev.id)
			s.log().Warn("delegate timeout", "id", ev.id, "to", ev.to, "after", d.String(), "action", "judge-gate")
			s.fallbackGate(loop, "judge timed out after "+d.String())
			continue // judge rounds always fail closed to a human, never notify-and-wait
		}
		action := e.role.TimeoutAction
		if action == "" {
			action = "notify"
		}
		s.log().Warn("delegate timeout", "id", ev.id, "to", ev.to, "after", d.String(), "action", action)
		if s.bellFn != nil {
			s.bellFn()
		}
		s.runHook(s.cfg.UI.OnTimeout, ev.to, ev.task)
		if action == "restart" && !e.exited {
			e.pane.Terminate() // SIGTERM; auto-restart takes over when restart = "on-failure"
		}
	}
}

// bootPanes injects each role's boot prompt once its pane settles, then verifies it landed and retries once.
func (s *session) bootPanes() {
	now := time.Now()
	for _, e := range s.panes {
		if e.exited {
			continue
		}
		if !e.booted {
			if now.Sub(e.startedAt) < bootMinWait || now.Sub(e.lastActive) < bootSettle {
				continue
			}
			s.log().Info("boot", "role", e.role.Name, "start", e.role.Start)
			s.injectBoot(e)
			e.booted = true
			e.bootSentAt = now
			e.bootTries = 1
			continue
		}
		if e.bootOK {
			continue
		}
		if bootLanded(e) {
			e.bootOK = true
			continue
		}
		if now.Sub(e.bootSentAt) < bootVerifyAfter {
			continue
		}
		if e.bootTries >= bootMaxTries {
			e.bootOK = true // give up quietly; the agent may have redrawn its screen
			s.log().Warn("boot injection unverified", "role", e.role.Name)
			continue
		}
		s.log().Warn("boot injection retry", "role", e.role.Name)
		injectLine(e, e.bootLine)
		e.bootTries++
		e.bootSentAt = now
	}
}

// bootLanded reports whether the boot one-liner is visible on the pane (wrap-safe join).
func bootLanded(e *entry) bool {
	if e.bootLine == "" {
		return true
	}
	snippet := []rune(e.bootLine)
	if len(snippet) > 24 {
		snippet = snippet[:24]
	}
	var b strings.Builder
	for _, l := range e.tailCached() {
		b.WriteString(l)
	}
	return strings.Contains(b.String(), string(snippet))
}

func (s *session) injectBoot(e *entry) {
	if e.role.Start {
		file := "orchestrator-context.md"
		e.bootLine = writeContext(file, prompt.OrchestratorContext(s.cfg),
			"Read "+filepath.Join(contextDir, file)+" for your role, available agents, and the delegation protocol. Acknowledge your role and wait for instructions.")
		injectLine(e, e.bootLine)
		return
	}
	file := sanitize(e.role.Name) + "-brief.md"
	e.bootLine = writeContext(file, prompt.WorkerBrief(e.role),
		"Read "+filepath.Join(contextDir, file)+" for your role, then stay idle until a task is delegated to you.")
	injectLine(e, e.bootLine)
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
		if errors.Is(err, pane.ErrPaneClosed) {
			e.exited = true
		}
		return // dropped input: the child is wedged, do not follow with a bare Enter
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

func (s *session) findRole(name string) (*entry, int) {
	for i, e := range s.panes {
		if e.role.Name == name && !e.gone {
			return e, i
		}
	}
	return nil, -1
}

func (s *session) startIdx() int {
	for i, e := range s.panes {
		if e.role.Start {
			return i
		}
	}
	return 0
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

// needsInput reports whether the pane's visible screen shows a blocking prompt.
func needsInput(e *entry) bool {
	if e.exited || e.pane == nil {
		return false
	}
	return promptInLines(lastN(e.tailCached(), 14), e.role.InputPrompts)
}

// promptInLines reports whether any line carries a built-in or role-configured blocking-prompt marker.
func promptInLines(lines []string, extra []string) bool {
	for _, l := range lines {
		low := strings.ToLower(l)
		for _, marker := range inputPrompts {
			if strings.Contains(low, marker) {
				return true
			}
		}
		for _, marker := range extra {
			if marker != "" && strings.Contains(low, strings.ToLower(marker)) {
				return true
			}
		}
	}
	return false
}

// restart replaces the role's process at cols x rows, resetting the auto-restart budget (user intervened).
func (s *session) restart(e *entry, idx, cols, rows int) {
	_ = e.pane.Close() // idempotent; unblocks the old stream so its exit is dropped by gen
	e.restarts = 0
	s.respawn(e, idx, cols, rows)
}

// autoRestart respawns a role that exited non-zero when its config asks for it, capped so a broken command cannot crash-loop.
func (s *session) autoRestart(e *entry, idx int) {
	if s.closed || e.gone || !e.role.RestartOnFailure() {
		if !s.closed && !e.gone {
			s.failLoopsFor(e.role.Name) // a judge that dies without supervision fails its loops closed
		}
		return
	}
	_ = e.pane.Close() // reap the child to capture its exit status
	if e.pane.ExitCode() == 0 {
		s.failLoopsFor(e.role.Name)
		return // clean exit is respected
	}
	if e.restarts >= e.role.RestartCap() {
		s.log().Warn("auto-restart cap reached", "role", e.role.Name, "restarts", e.restarts)
		s.failLoopsFor(e.role.Name)
		return
	}
	e.restarts++
	s.log().Warn("auto-restart", "role", e.role.Name, "attempt", e.restarts, "exit", e.pane.ExitCode())
	cw, ch := e.pane.Size() // respawn at the pane's current size; resizePanes syncs visible tiles anyway
	s.respawn(e, idx, cw, ch)
}

// respawn replaces an entry's pane with a fresh process at cols x rows, resetting boot state; gen drops the old stream.
func (s *session) respawn(e *entry, idx, cols, rows int) {
	p, err := startRole(e.role, cols, rows, roleEnv(e.role, s.socket, s.baseURL))
	if err != nil {
		e.exited = true
		s.log().Error("restart failed", "role", e.role.Name, "err", err)
		return
	}
	e.pane = p
	e.gen++
	e.exited = false
	e.booted = false
	e.paused = false
	e.bootOK = false
	e.bootTries = 0
	e.bootLine = ""
	e.startedAt = time.Now()
	e.lastActive = time.Now()
	s.log().Info("role restarted", "role", e.role.Name)
	s.watchPane(e, idx)
}

// reload re-reads the config file and converges the roster on it: spawn added roles, tombstone
// removed ones, respawn changed specs. New panes spawn at cw x ch. The start role's process is
// never touched. It returns the tombstoned indices (the UI closes their tiles) and whether
// anything changed (the UI resizes).
func (s *session) reload(cw, ch int) (retired []int, changed bool) {
	if s.cfg.Path == "" {
		s.log().Warn("reload refused: running on the built-in config (no file to reload)")
		return nil, false
	}
	cfg, err := config.Load(s.cfg.Path)
	if err != nil {
		s.log().Error("reload failed", "err", err)
		return nil, false
	}
	for _, w := range cfg.Warnings {
		s.log().Warn("config", "warning", w)
	}
	startName := s.panes[s.startIdx()].role.Name
	next := make(map[string]bool, len(cfg.Roles))
	for _, r := range cfg.Roles {
		next[r.Name] = true
		if r.Start != (r.Name == startName) {
			s.log().Warn("reload: start role reassignment ignored (restart the deck to apply)", "role", r.Name)
		}
	}

	var added, removed, respawned []string
	// retire roles the file dropped; removal is the user's explicit decision, in-flight or not
	for i, e := range s.panes {
		if e.gone || next[e.role.Name] {
			continue
		}
		if e.role.Name == startName {
			s.log().Warn("reload: refusing to remove the start role", "role", startName)
			continue
		}
		s.retireRole(e)
		retired = append(retired, i)
		removed = append(removed, e.role.Name)
	}
	// converge existing roles and spawn new ones, in file order
	for _, r := range cfg.Roles {
		e, i := s.findRole(r.Name)
		switch {
		case e == nil:
			if _, err := exec.LookPath(r.Command); err != nil {
				s.log().Error("reload: new role command not found", "role", r.Name, "command", r.Command)
				continue
			}
			s.spawnRole(r, cw, ch)
			added = append(added, r.Name)
		case r.Name == startName:
			if specChanged(e.role, r) {
				s.log().Warn("reload: start role spec changes ignored (restart the deck to apply)", "role", startName)
			}
			e.role = softMerge(e.role, r) // prompt/approve/restart changes still land
		case !specChanged(e.role, r):
			r.Start = false
			e.role = r // no process identity change; takes effect on the next task
		case s.tasksInFlight(r.Name):
			s.log().Warn("reload: respawn skipped, tasks in flight (rerun once resolved)", "role", r.Name)
		default:
			if _, err := exec.LookPath(r.Command); err != nil {
				s.log().Error("reload: changed command not found, keeping the old process", "role", r.Name, "command", r.Command)
				continue
			}
			r.Start = false
			e.role = r
			_ = e.pane.Close()
			e.restarts = 0
			pcw, pch := e.pane.Size()
			s.respawn(e, i, pcw, pch)
			respawned = append(respawned, r.Name)
		}
	}
	// pending gates for tombstoned roles can never be delivered; drop them loudly
	kept := s.gates[:0]
	for _, g := range s.gates {
		if e, _ := s.findRole(g.to); e != nil {
			kept = append(kept, g)
		} else {
			s.log().Warn("reload: dropped pending gate for removed role", "to", g.to)
		}
	}
	s.gates = kept
	s.cfg.Roles = cfg.Roles // future orchestrator boots see the new roster
	if len(added)+len(removed)+len(respawned) == 0 {
		s.log().Info("reload: no role changes")
		return retired, false
	}
	s.log().Info("reload applied",
		"added", strings.Join(added, ","), "removed", strings.Join(removed, ","), "respawned", strings.Join(respawned, ","))
	// the orchestrator's boot context listed the old team; tell it the roster moved
	var parts []string
	for _, n := range added {
		parts = append(parts, "+"+n)
	}
	for _, n := range removed {
		parts = append(parts, "-"+n)
	}
	if len(parts) > 0 {
		if st := s.panes[s.startIdx()]; !st.exited {
			injectLine(st, "[choragos] Team changed: "+strings.Join(parts, ", ")+". Delegate accordingly.")
		}
	}
	return retired, true
}

// retireRole tombstones a reload-removed role: graceful stop off the UI loop, its index kept.
func (s *session) retireRole(e *entry) {
	e.gone = true
	e.exited = true
	s.log().Info("role removed", "role", e.role.Name)
	p := e.pane
	go func() {
		p.Terminate()
		p.Shutdown(time.Now().Add(gracefulTimeout))
	}()
}

// spawnRole appends a reload-added role at cw x ch and watches it; boot injection follows on the next ticks.
func (s *session) spawnRole(r config.Role, cw, ch int) {
	r.Start = false
	p, err := startRole(r, cw, ch, roleEnv(r, s.socket, s.baseURL))
	if err != nil {
		s.log().Error("reload: role start failed", "role", r.Name, "err", err)
		return
	}
	e := &entry{role: r, pane: p}
	e.startedAt = time.Now()
	e.lastActive = time.Now()
	s.panes = append(s.panes, e)
	s.watchPane(e, len(s.panes)-1)
	s.log().Info("role added", "role", r.Name)
}

// specChanged reports whether a role change needs a process restart (command line or env identity).
func specChanged(a, b config.Role) bool {
	return a.Command != b.Command || a.Model != b.Model ||
		!slices.Equal(a.Args, b.Args) ||
		!slices.Equal(a.EnvAllow, b.EnvAllow) ||
		!slices.Equal(a.EnvDeny, b.EnvDeny)
}

// softMerge keeps old's process identity and takes upd's restart-free fields.
func softMerge(old, upd config.Role) config.Role {
	old.Prompt = upd.Prompt
	old.Approve = upd.Approve
	old.Restart = upd.Restart
	old.RestartRetries = upd.RestartRetries
	old.InputPrompts = upd.InputPrompts
	old.ChromeMarkers = upd.ChromeMarkers
	return old
}

// tasksInFlight reports whether the role has a pending gate or an unresolved delegation.
func (s *session) tasksInFlight(name string) bool {
	for _, g := range s.gates {
		if g.to == name {
			return true
		}
	}
	for _, ev := range s.board {
		if ev.kind == "delegate" && ev.to == name && ev.doneAt.IsZero() {
			return true
		}
	}
	return false
}

// gatewayBlocked reports whether fail-closed enforcement should refuse dispatch (on, fail-closed, and down).
func (s *session) gatewayBlocked() bool {
	return s.sphragisOn && s.cfg.Sphragis.IsFailClosed() && !s.gatewayUp
}

func (s *session) closeAll() {
	if s.closed {
		return
	}
	s.closed = true
	s.log().Info("deck stopping")
	if s.server != nil {
		_ = s.server.Close()
		_ = os.Remove(s.socket)
		ipc.RemoveMeta()
	}
	// SIGTERM every agent first so they all run their SessionEnd hooks in parallel, then force after the shared deadline.
	deadline := time.Now().Add(gracefulTimeout)
	for _, e := range s.panes {
		e.pane.Terminate()
	}
	for _, e := range s.panes {
		e.pane.Shutdown(deadline)
	}
	// final cumulative snapshot so the report sees tokens burned since the last tick
	if s.sphragisOn && s.gatewayUp {
		s.logTokens()
	}
	_ = s.gateway.Close()
	if s.eventsC != nil {
		_ = s.eventsC.Close()
	}
}

// baselineEnv are the vars an agent needs to run at all, always kept in allowlist mode.
var baselineEnv = []string{"PATH", "HOME", "TERM", "COLORTERM", "USER", "LOGNAME", "SHELL", "PWD", "TMPDIR", "LANG", "LC_*", "XDG_*"}

// roleEnv builds one role's child env: the full env by default, baseline plus
// env_allow when set, minus env_deny; choragos's own vars are always appended.
func roleEnv(r config.Role, socket, baseURL string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !envAllowed(name, r) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, ipc.EnvSocket+"="+socket)
	if baseURL != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+agentURL(baseURL, r.Name))
	}
	return env
}

// envAllowed applies env_deny first, then the allowlist (baseline + env_allow) when one is set.
func envAllowed(name string, r config.Role) bool {
	if matchEnv(name, r.EnvDeny) {
		return false
	}
	if len(r.EnvAllow) == 0 {
		return true
	}
	return matchEnv(name, baselineEnv) || matchEnv(name, r.EnvAllow)
}

// matchEnv reports whether name matches any pattern: exact, or a "PREFIX_*" wildcard.
func matchEnv(name string, patterns []string) bool {
	for _, p := range patterns {
		if pre, ok := strings.CutSuffix(p, "*"); ok {
			if pre != "" && strings.HasPrefix(name, pre) {
				return true
			}
			continue
		}
		if name == p {
			return true
		}
	}
	return false
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
	var entries []*entry
	for _, r := range cfg.Roles {
		p, err := startRole(r, cols, rows, roleEnv(r, socket, baseURL))
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

// openLog opens a per-role transcript log under contextDir/logs; logging is best-effort so failures are silent.
// Append mode with a session header, so a role restart does not truncate the previous session's transcript.
func openLog(role string) *os.File {
	dir := filepath.Join(contextDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, sanitize(role)+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	wd, _ := os.Getwd()
	fmt.Fprintf(f, "--- choragos transcript · role=%s · dir=%s · started=%s ---\n", role, wd, time.Now().Format(time.RFC3339))
	return f
}

// writeCrashLog dumps the panic and stack to contextDir/logs; returns where it landed.
func writeCrashLog(r any) string {
	dir := filepath.Join(contextDir, "logs")
	body := fmt.Sprintf("time: %s\npanic: %v\n\n%s", time.Now().Format(time.RFC3339), r, debug.Stack())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, body)
		return "stderr"
	}
	path := filepath.Join(dir, "crash.log")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, body)
		return "stderr"
	}
	return path
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
