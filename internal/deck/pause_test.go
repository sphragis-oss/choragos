// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestTogglePauseFreezesAndResumes(t *testing.T) {
	panes := startCatPanes(t, "orchestrator", "coder")
	m := newTestModel(panes)
	var buf strings.Builder
	m.events = slog.New(slog.NewTextHandler(&buf, nil))

	m.togglePause(1)
	if !m.panes[1].paused {
		t.Fatal("role should be paused")
	}
	st := computeStatus(m.panes[1], time.Now(), m.th)
	if st.label != "paused" {
		t.Fatalf("status label = %q, want paused", st.label)
	}
	m.togglePause(1)
	if m.panes[1].paused {
		t.Fatal("role should be resumed")
	}
	for _, want := range []string{"role paused", "role resumed"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("log missing %q:\n%s", want, buf.String())
		}
	}
}

func TestPausedRoleSkipsTimeoutAndWaiting(t *testing.T) {
	panes := startCatPanes(t, "orchestrator", "coder")
	m := newTestModel(panes)
	m.panes[1].role.Timeout = "10ms"
	m.recordTask(expiredDelegate("coder"))
	m.togglePause(1)
	m.checkTimeouts()
	m.checkWaiting()
	if m.board[0].timedOut {
		t.Fatal("paused role must not accrue timeouts")
	}
	if m.panes[1].waiting {
		t.Fatal("paused role must not read as waiting")
	}
}

func TestCreditPauseShiftsOpenDelegations(t *testing.T) {
	panes := startCatPanes(t, "orchestrator", "coder")
	m := newTestModel(panes)
	start := time.Now().Add(-10 * time.Second)
	m.recordTask(taskEvent{at: start, kind: "delegate", id: "T1", to: "coder", task: "x"})
	// paused for the last ~5s of those 10
	m.creditPause("coder", time.Now().Add(-5*time.Second))
	shifted := m.board[0].at
	if d := shifted.Sub(start); d < 4*time.Second || d > 6*time.Second {
		t.Fatalf("pause credit shifted by %v, want ~5s", d)
	}
	// a delegation to another role is untouched
	m.recordTask(taskEvent{at: start, kind: "delegate", id: "T2", to: "orchestrator", task: "y"})
	m.creditPause("coder", time.Now())
	if !m.board[1].at.Equal(start) {
		t.Fatal("other roles' delegations must not shift")
	}
}
