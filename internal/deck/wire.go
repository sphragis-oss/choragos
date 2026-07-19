// SPDX-License-Identifier: Apache-2.0

// Session-side conversions to the attach protocol (internal/wire).
package deck

import (
	"github.com/sphragis-oss/choragos/internal/wire"
)

// toWireTasks converts the board for the wire.
func toWireTasks(board []taskEvent) []wire.Task {
	out := make([]wire.Task, 0, len(board))
	for _, ev := range board {
		w := wire.Task{At: ev.at.UnixNano(), Kind: ev.kind, ID: ev.id, To: ev.to, Task: ev.task, File: ev.file, Done: ev.done, Round: ev.round, Score: ev.score, TimedOut: ev.timedOut}
		if !ev.doneAt.IsZero() {
			w.DoneAt = ev.doneAt.UnixNano()
		}
		out = append(out, w)
	}
	return out
}

// toWireGates converts the pending gates for the wire.
func toWireGates(gates []pendingGate) []wire.Gate {
	out := make([]wire.Gate, 0, len(gates))
	for _, g := range gates {
		out = append(out, wire.Gate{Cmd: g.cmd, To: g.to, At: g.at.UnixNano(), Reason: g.reason, Report: g.report})
	}
	return out
}

// snapshotEvents builds the state events every client sync needs: roster, board, gates, status.
func (s *session) snapshotEvents() []wire.Event {
	roster := make([]wire.Role, 0, len(s.panes))
	for _, e := range s.panes {
		roster = append(roster, wire.Role{Role: e.role, Exited: e.exited, Gone: e.gone, Waiting: e.waiting, Paused: e.paused, OverBudget: e.overBudget, Restarts: e.restarts})
	}
	return []wire.Event{
		{Kind: "roster", Roster: roster},
		{Kind: "board", Board: toWireTasks(s.board)},
		{Kind: "gates", Gates: toWireGates(s.gates)},
		{Kind: "status", On: s.sphragisOn, Up: s.gatewayUp},
	}
}
