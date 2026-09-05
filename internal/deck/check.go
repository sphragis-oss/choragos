// SPDX-License-Identifier: Apache-2.0

// Deterministic check gate: a shell command run on the builder's work-done before
// the judge, off the loop thread, retrying on non-zero exit (see docs/design-check-gate.md).
package deck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// checkOutputCap bounds the output tail written to the check report file.
const checkOutputCap = 32 * 1024

// checkMsg carries one finished check run back into the loop thread.
type checkMsg struct {
	id   string // in-flight task id the loop is keyed by
	exit int    // process exit status; meaningless when fail is set
	fail string // why the command could not run to completion; empty on a real exit
	file string // output report path
	took time.Duration
}

// startCheck runs the builder's check command off the loop thread; the result re-enters as a checkMsg.
func (s *session) startCheck(loop *judgeLoop, builder *entry, id string) {
	dir := "."
	if builder.role.Worktree {
		dir = WorktreePath(builder.role.Name)
	}
	loop.phase = "check"
	s.loops[id] = loop
	file := filepath.Join(contextDir, fmt.Sprintf("check-%s-r%d.log", loop.origID, loop.round))
	cmd, timeout := builder.role.Check, builder.role.CheckTimeoutDuration()
	s.log().Info("check start", "loop", loop.origID, "round", loop.round, "dir", dir, "command", cmd)
	go func() { s.send(runCheck(id, cmd, dir, file, timeout)) }()
}

// runCheck executes the command in dir with a process-group kill on timeout and writes the output tail to file.
func runCheck(id, command, dir, file string, timeout time.Duration) checkMsg {
	t0 := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Dir = dir
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = time.Second
	out, err := c.CombinedOutput()
	m := checkMsg{id: id, file: file, took: time.Since(t0).Round(time.Millisecond)}
	var exitErr *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		m.fail = "check timed out after " + timeout.String()
	case errors.As(err, &exitErr):
		m.exit = exitErr.ExitCode()
	case err != nil:
		m.fail = "check unavailable: " + err.Error()
	}
	if len(out) > checkOutputCap {
		out = out[len(out)-checkOutputCap:]
	}
	status := fmt.Sprintf("exit %d", m.exit)
	if m.fail != "" {
		status = m.fail
	}
	head := fmt.Sprintf("# check: %s\n# dir: %s\n# %s (took %s)\n\n", command, dir, status, m.took)
	if werr := os.WriteFile(file, append([]byte(head), out...), 0o644); werr != nil && m.fail == "" {
		m.fail = "check unavailable: " + werr.Error()
	}
	return m
}

// finishCheck advances the loop owning a finished check: pass to the judge or accept, fail to retry or a human gate.
func (s *session) finishCheck(msg checkMsg) {
	loop, ok := s.loops[msg.id]
	if !ok || loop.phase != "check" {
		s.log().Warn("check result for unknown loop", "id", msg.id, "file", msg.file)
		return
	}
	delete(s.loops, msg.id)
	builder, _ := s.findRole(loop.builder)
	if builder == nil {
		loop.report = msg.file
		s.fallbackGate(loop, "builder role is gone")
		return
	}
	if msg.fail != "" {
		s.log().Warn("check", "loop", loop.origID, "round", loop.round, "verdict", "unavailable", "reason", msg.fail, "took", msg.took)
		s.annotateTask(msg.id, loop.round, "check "+msg.fail)
		loop.report = msg.file
		s.fallbackGate(loop, msg.fail)
		return
	}
	pass := msg.exit == 0
	s.log().Info("check", "loop", loop.origID, "round", loop.round, "exit", msg.exit, "verdict", map[bool]string{true: "pass", false: "fail"}[pass], "took", msg.took)
	if pass {
		s.annotateTask(msg.id, loop.round, "check ok")
		loop.checkLog = msg.file
		if builder.role.Judge != "" {
			s.deliverJudgeRound(loop)
			return
		}
		s.notifyOrchestrator(fmt.Sprintf("[choragos] %s passed the check for task %s (round %d). Report: read %s",
			loop.builder, loop.origID, loop.round, loop.report))
		s.queueMerge(loop.builder, loop.origID)
		return
	}
	s.annotateTask(msg.id, loop.round, fmt.Sprintf("check exit %d", msg.exit))
	loop.report = msg.file
	if loop.round >= builder.role.JudgeCap() {
		s.fallbackGate(loop, fmt.Sprintf("check cap exhausted after round %d, last exit %d", loop.round, msg.exit))
		return
	}
	loop.round++
	loop.phase = "build"
	s.deliverRetryRound(loop, builder, fmt.Sprintf("Your previous attempt failed the check command (exit %d). Read %s for its output, fix the cause, and redo the task.", msg.exit, msg.file))
}
