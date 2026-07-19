// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func expiredDelegate(to string) taskEvent {
	return taskEvent{at: time.Now().Add(-time.Second), kind: "delegate", id: "T1", to: to, task: "smoke"}
}

func TestCheckTimeoutsNotifyFiresOnce(t *testing.T) {
	panes := startCatPanes(t, "orchestrator", "coder")
	m := newTestModel(panes)
	m.panes[1].role.Timeout = "10ms"
	var buf strings.Builder
	m.events = slog.New(slog.NewTextHandler(&buf, nil))
	bells := 0
	m.bellFn = func() { bells++ }
	m.recordTask(expiredDelegate("coder"))

	m.checkTimeouts()
	m.checkTimeouts() // second tick must not re-fire
	if !m.board[0].timedOut {
		t.Fatal("expired delegation not marked timedOut")
	}
	if bells != 1 || strings.Count(buf.String(), "delegate timeout") != 1 {
		t.Fatalf("timeout must fire exactly once: bells=%d log=%q", bells, buf.String())
	}
	if !strings.Contains(buf.String(), "action=notify") {
		t.Fatalf("default action should be notify: %q", buf.String())
	}
}

func TestCheckTimeoutsSkipsHealthyAndResolved(t *testing.T) {
	panes := startCatPanes(t, "orchestrator", "coder")
	m := newTestModel(panes)
	m.panes[1].role.Timeout = "1h"
	m.recordTask(expiredDelegate("coder")) // 1s old, 1h budget
	done := expiredDelegate("coder")
	done.id, done.doneAt = "T2", time.Now()
	m.recordTask(done) // resolved before expiry check
	m.checkTimeouts()
	if m.board[0].timedOut || m.board[1].timedOut {
		t.Fatalf("healthy or resolved delegations must not time out: %+v", m.board)
	}
}

func TestCheckTimeoutsNoTimeoutConfigured(t *testing.T) {
	panes := startCatPanes(t, "orchestrator", "coder")
	m := newTestModel(panes)
	m.recordTask(expiredDelegate("coder"))
	m.checkTimeouts()
	if m.board[0].timedOut {
		t.Fatal("roles without a timeout must never expire")
	}
}

func TestCheckTimeoutsRestartTerminatesWorker(t *testing.T) {
	panes := startCatPanes(t, "orchestrator", "coder")
	m := newTestModel(panes)
	m.panes[1].role.Timeout = "10ms"
	m.panes[1].role.TimeoutAction = "restart"
	m.recordTask(expiredDelegate("coder"))

	exited := make(chan struct{})
	p := m.panes[1].pane
	go func() { _ = p.Stream(nil); close(exited) }()
	m.checkTimeouts()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout_action=restart did not terminate the worker")
	}
}

func TestToWireTasksCarriesTimeout(t *testing.T) {
	ev := expiredDelegate("coder")
	ev.timedOut = true
	w := toWireTasks([]taskEvent{ev})
	if len(w) != 1 || !w[0].TimedOut {
		t.Fatalf("wire task = %+v, want TimedOut", w)
	}
}
