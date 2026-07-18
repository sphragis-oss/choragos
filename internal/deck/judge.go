// SPDX-License-Identifier: Apache-2.0

// Autonomous judge loop: deck-synthesized builder -> judge rounds gated by a
// file-based VERDICT, failing closed to a human gate on any ambiguity.
package deck

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/prompt"
)

// judgeLoop tracks one delegation moving through builder -> judge rounds.
type judgeLoop struct {
	origID  string      // first delegation's task id, the loop's identity in logs
	builder string      // role whose work is judged
	cmd     ipc.Command // original delegation (task text + brief path)
	round   int         // 1-based
	phase   string      // "build" | "judge"
	report  string      // latest report path: builder's in judge phase, judge's on fallback
}

// verdictCap bounds the judge report read; the VERDICT line must lead the file.
const verdictCap = 64 * 1024

// parseVerdict returns the score from the report's first non-empty line, strictly "VERDICT: <n>/10".
func parseVerdict(path string) (int, error) {
	if path == "" {
		return 0, errors.New("work-done carried no --report")
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, verdictCap))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rest, ok := strings.CutPrefix(line, "VERDICT:")
		if !ok {
			return 0, fmt.Errorf("first line %q is not \"VERDICT: <n>/10\"", line)
		}
		num, ok := strings.CutSuffix(strings.TrimSpace(rest), "/10")
		if !ok {
			return 0, fmt.Errorf("verdict %q is not \"<n>/10\"", strings.TrimSpace(rest))
		}
		n, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil || n < 0 || n > 10 {
			return 0, fmt.Errorf("verdict score %q is not an integer 0-10", strings.TrimSpace(num))
		}
		return n, nil
	}
	return 0, errors.New("report is empty")
}

// maybeStartLoop registers a judge loop for a just-delivered delegation when the role declares one.
func (s *session) maybeStartLoop(id string, e *entry, cmd ipc.Command) {
	if e.role.Judge == "" || id == "" {
		return
	}
	if s.loops == nil {
		s.loops = make(map[string]*judgeLoop)
	}
	s.loops[id] = &judgeLoop{origID: id, builder: e.role.Name, cmd: cmd, round: 1, phase: "build"}
	s.annotateTask(id, 1, "")
	s.log().Info("judge loop started", "loop", id, "builder", e.role.Name, "judge", e.role.Judge, "cap", e.role.JudgeCap(), "pass", e.role.JudgePassScore())
}

// annotateTask stamps round and score onto the newest delegate row with this id.
func (s *session) annotateTask(id string, round int, score string) {
	for i := len(s.board) - 1; i >= 0; i-- {
		if s.board[i].id == id && s.board[i].kind == "delegate" {
			s.board[i].round = round
			if score != "" {
				s.board[i].score = score
			}
			return
		}
	}
}

// handleJudgedDone advances the loop owning this work-done; false means no loop owns it.
func (s *session) handleJudgedDone(cmd ipc.Command) bool {
	loop, ok := s.loops[cmd.ID]
	if !ok {
		return false
	}
	delete(s.loops, cmd.ID)
	from := loop.builder
	if loop.phase == "judge" {
		from = s.judgeName(loop)
	}
	s.recordTask(taskEvent{at: time.Now(), kind: "work-done", id: cmd.ID, to: from, task: singleLine(cmd.Task), file: cmd.Report, round: loop.round})
	s.resolveTask(cmd.ID)
	if loop.phase == "build" {
		loop.report = cmd.Report
		s.deliverJudgeRound(loop)
		return true
	}
	s.scoreVerdict(loop, cmd)
	return true
}

// judgeName resolves the builder's configured judge; empty when the builder is gone.
func (s *session) judgeName(loop *judgeLoop) string {
	if e, _ := s.findRole(loop.builder); e != nil {
		return e.role.Judge
	}
	return ""
}

// deliverJudgeRound hands the builder's report to the judge with the strict verdict contract.
func (s *session) deliverJudgeRound(loop *judgeLoop) {
	builder, _ := s.findRole(loop.builder)
	if builder == nil {
		s.fallbackGate(loop, "builder role is gone")
		return
	}
	e, i := s.findRole(builder.role.Judge)
	if e == nil || e.exited || e.gone {
		s.fallbackGate(loop, "judge unavailable")
		return
	}
	s.taskSeq++
	id := fmt.Sprintf("T%d", s.taskSeq)
	verdictFile, err := filepath.Abs(filepath.Join(contextDir, fmt.Sprintf("judge-verdict-%s-r%d.md", loop.origID, loop.round)))
	if err != nil {
		verdictFile = filepath.Join(contextDir, fmt.Sprintf("judge-verdict-%s-r%d.md", loop.origID, loop.round))
	}
	task := loop.cmd.Task
	if loop.cmd.Brief != "" {
		task = strings.TrimSpace("Read " + loop.cmd.Brief + " for the full brief.\n\n" + task)
	}
	file := "judge-task-" + sanitize(e.role.Name) + ".md"
	line := writeContext(file, prompt.JudgeTask(e.role, task, loop.report, verdictFile, id, builder.role.JudgePassScore()),
		"Read "+filepath.Join(contextDir, file)+" for your task.")
	label := fmt.Sprintf("judge %s round %d", loop.origID, loop.round)
	s.log().Info("delegate", "id", id, "from", "choragos", "to", e.role.Name, "task", label, "loop", loop.origID, "round", loop.round)
	s.recordTask(taskEvent{at: time.Now(), kind: "delegate", id: id, to: e.role.Name, task: label, file: loop.report, round: loop.round})
	s.snapshotTask(id, e.role.Name, label)
	injectLine(e, line)
	s.focus(i)
	loop.phase = "judge"
	s.loops[id] = loop
}

// scoreVerdict parses the judge's report and passes, retries, or fails closed.
func (s *session) scoreVerdict(loop *judgeLoop, cmd ipc.Command) {
	builder, _ := s.findRole(loop.builder)
	if builder == nil {
		s.fallbackGate(loop, "builder role is gone")
		return
	}
	score, err := parseVerdict(cmd.Report)
	if err != nil {
		loop.report = cmd.Report
		s.log().Warn("judge", "loop", loop.origID, "round", loop.round, "verdict", "invalid", "err", err)
		s.fallbackGate(loop, "unparseable verdict: "+err.Error())
		return
	}
	scoreStr := fmt.Sprintf("%d/10", score)
	pass := score >= builder.role.JudgePassScore()
	s.annotateTask(cmd.ID, loop.round, scoreStr)
	s.log().Info("judge", "loop", loop.origID, "round", loop.round, "score", score, "verdict", map[bool]string{true: "pass", false: "fail"}[pass])
	loop.report = cmd.Report
	if pass {
		s.notifyOrchestrator(fmt.Sprintf("[choragos] %s passed judge review for task %s (round %d, score %s). Full verdict: read %s",
			loop.builder, loop.origID, loop.round, scoreStr, cmd.Report))
		return
	}
	if loop.round >= builder.role.JudgeCap() {
		s.fallbackGate(loop, fmt.Sprintf("judge cap exhausted after round %d, last score %s", loop.round, scoreStr))
		return
	}
	loop.round++
	loop.phase = "build"
	s.deliverRetryRound(loop, builder, scoreStr)
}

// deliverRetryRound re-delegates the original task to the builder with the judge's critique.
func (s *session) deliverRetryRound(loop *judgeLoop, builder *entry, score string) {
	e, i := s.findRole(loop.builder)
	if e == nil || e.exited || e.gone {
		s.fallbackGate(loop, "builder unavailable for retry")
		return
	}
	retry := ipc.Command{
		Task: fmt.Sprintf("Your previous attempt scored %s, below the passing score of %d. Read %s for the judge's critique, address every point, and redo the task.\n\nOriginal task:\n%s",
			score, builder.role.JudgePassScore(), loop.report, loop.cmd.Task),
		Brief: loop.cmd.Brief,
	}
	id := s.deliverDelegate(e, i, retry)
	s.annotateTask(id, loop.round, "")
	s.log().Info("judge retry", "loop", loop.origID, "round", loop.round, "id", id)
	s.loops[id] = loop
}

// fallbackGate fails the loop closed into the human approval queue.
func (s *session) fallbackGate(loop *judgeLoop, reason string) {
	s.gates = append(s.gates, pendingGate{cmd: loop.cmd, to: loop.builder, at: time.Now(), reason: reason, report: loop.report, loopID: loop.origID})
	s.log().Warn("judge gate", "loop", loop.origID, "round", loop.round, "reason", reason, "report", loop.report)
	if s.bellFn != nil {
		s.bellFn()
	}
	s.runHook(s.cfg.UI.OnGate, loop.builder, "judge loop halted: "+reason)
}

// resolveFallback closes a judge fallback gate: accept hands the result to the orchestrator, reject asks for a revise.
func (s *session) resolveFallback(g pendingGate, accept bool) {
	s.log().Info("judge gate resolved", "loop", g.loopID, "to", g.to, "accepted", accept, "reason", g.reason)
	if accept {
		msg := fmt.Sprintf("[choragos] The user accepted %s's work for task %s despite: %s.", g.to, g.loopID, g.reason)
		if g.report != "" {
			msg += " Last report: read " + g.report
		}
		s.notifyOrchestrator(msg)
		return
	}
	s.notifyOrchestrator(fmt.Sprintf("[choragos] The judge loop for your delegation to %s stopped (%s) and the user rejected the result. Revise the task or the brief and delegate again if still needed.", g.to, g.reason))
}

// notifyOrchestrator injects one line into the start role's pane.
func (s *session) notifyOrchestrator(line string) {
	if i := s.startIdx(); i >= 0 && i < len(s.panes) && !s.panes[i].exited {
		injectLine(s.panes[i], line)
		s.focus(i)
	}
}

// failLoopsFor fails closed every judge-phase loop waiting on this role's pane.
func (s *session) failLoopsFor(role string) {
	for id, loop := range s.loops {
		if loop.phase != "judge" {
			continue
		}
		if s.judgeName(loop) != role {
			continue
		}
		delete(s.loops, id)
		s.fallbackGate(loop, "judge exited")
	}
}
