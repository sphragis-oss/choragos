// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
)

// wireProto is bumped on any breaking change to the attach protocol; no negotiation, match or refuse.
const wireProto = 1

// maxWireFrame bounds one frame so a corrupt length prefix cannot allocate unbounded memory.
const maxWireFrame = 4 << 20

// Frame kinds on the UI socket: raw pane output vs JSON events.
const (
	kindOutput byte = 'o'
	kindEvent  byte = 'e'
)

// wireEvent is the single JSON envelope for everything that is not pane output.
type wireEvent struct {
	Kind string `json:"kind"`
	// hello (client -> server)
	Proto   int    `json:"proto,omitempty"`
	Version string `json:"version,omitempty"`
	// welcome (server -> client)
	Cfg      *config.Config `json:"cfg,omitempty"`
	Roster   []wireRole     `json:"roster,omitempty"`
	Board    []wireTask     `json:"board,omitempty"`
	Gates    []wireGate     `json:"gates,omitempty"`
	Layout   []byte         `json:"layout,omitempty"`
	SnapSeqs []uint64       `json:"snap_seqs,omitempty"`
	// busy (server -> client)
	PID int `json:"pid,omitempty"`
	// input/resize/restart/focus and friends
	Idx     int    `json:"idx,omitempty"`
	Data    []byte `json:"data,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Approve bool   `json:"approve,omitempty"`
	On      bool   `json:"on,omitempty"` // status: sphragis enforcement / gateway health
	Up      bool   `json:"up,omitempty"`
}

// wireRole mirrors one roster entry for the client.
type wireRole struct {
	Role     config.Role `json:"role"`
	Exited   bool        `json:"exited"`
	Gone     bool        `json:"gone"`
	Waiting  bool        `json:"waiting"`
	Paused   bool        `json:"paused,omitempty"`
	Restarts int         `json:"restarts"`
}

// wireTask mirrors one task-board event.
type wireTask struct {
	At     int64  `json:"at"` // unix nanos
	Kind   string `json:"k"`
	ID     string `json:"id,omitempty"`
	To     string `json:"to,omitempty"`
	Task   string `json:"t,omitempty"`
	File   string `json:"f,omitempty"`
	Done   bool   `json:"d,omitempty"`
	DoneAt int64  `json:"da,omitempty"`
}

// wireGate mirrors one pending approval gate.
type wireGate struct {
	Cmd ipc.Command `json:"cmd"`
	To  string      `json:"to"`
	At  int64       `json:"at"`
}

// wireConn frames messages on the UI socket: [uint32 len][kind byte][payload].
type wireConn struct {
	c  net.Conn
	wr sync.Mutex
}

func newWireConn(c net.Conn) *wireConn { return &wireConn{c: c} }

func (w *wireConn) writeFrame(kind byte, payload ...[]byte) error {
	w.wr.Lock()
	defer w.wr.Unlock()
	n := 1
	for _, p := range payload {
		n += len(p)
	}
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(n))
	hdr[4] = kind
	if _, err := w.c.Write(hdr[:]); err != nil {
		return err
	}
	for _, p := range payload {
		if _, err := w.c.Write(p); err != nil {
			return err
		}
	}
	return nil
}

// WriteOutput ships one pane chunk: payload is [idx byte][raw bytes].
func (w *wireConn) WriteOutput(idx int, chunk []byte) error {
	return w.writeFrame(kindOutput, []byte{byte(idx)}, chunk)
}

// WriteEvent ships one JSON event.
func (w *wireConn) WriteEvent(ev wireEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return w.writeFrame(kindEvent, b)
}

// Read returns the next frame: for output, idx and chunk; for events, the decoded event.
func (w *wireConn) Read() (kind byte, idx int, chunk []byte, ev wireEvent, err error) {
	var hdr [5]byte
	if _, err = io.ReadFull(w.c, hdr[:]); err != nil {
		return 0, 0, nil, wireEvent{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n < 1 || n > maxWireFrame {
		return 0, 0, nil, wireEvent{}, fmt.Errorf("wire: bad frame length %d", n)
	}
	kind = hdr[4]
	payload := make([]byte, n-1)
	if _, err = io.ReadFull(w.c, payload); err != nil {
		return 0, 0, nil, wireEvent{}, err
	}
	switch kind {
	case kindOutput:
		if len(payload) < 1 {
			return 0, 0, nil, wireEvent{}, fmt.Errorf("wire: empty output frame")
		}
		return kind, int(payload[0]), payload[1:], wireEvent{}, nil
	case kindEvent:
		if err = json.Unmarshal(payload, &ev); err != nil {
			return 0, 0, nil, wireEvent{}, fmt.Errorf("wire: bad event: %w", err)
		}
		return kind, 0, nil, ev, nil
	default:
		return 0, 0, nil, wireEvent{}, fmt.Errorf("wire: unknown frame kind %q", kind)
	}
}

func (w *wireConn) Close() error { return w.c.Close() }

// toWireTasks converts the board for the wire.
func toWireTasks(board []taskEvent) []wireTask {
	out := make([]wireTask, 0, len(board))
	for _, ev := range board {
		w := wireTask{At: ev.at.UnixNano(), Kind: ev.kind, ID: ev.id, To: ev.to, Task: ev.task, File: ev.file, Done: ev.done}
		if !ev.doneAt.IsZero() {
			w.DoneAt = ev.doneAt.UnixNano()
		}
		out = append(out, w)
	}
	return out
}

// toWireGates converts the pending gates for the wire.
func toWireGates(gates []pendingGate) []wireGate {
	out := make([]wireGate, 0, len(gates))
	for _, g := range gates {
		out = append(out, wireGate{Cmd: g.cmd, To: g.to, At: g.at.UnixNano()})
	}
	return out
}

// snapshotEvents builds the state events every client sync needs: roster, board, gates, status.
func (s *session) snapshotEvents() []wireEvent {
	roster := make([]wireRole, 0, len(s.panes))
	for _, e := range s.panes {
		roster = append(roster, wireRole{Role: e.role, Exited: e.exited, Gone: e.gone, Waiting: e.waiting, Paused: e.paused, Restarts: e.restarts})
	}
	return []wireEvent{
		{Kind: "roster", Roster: roster},
		{Kind: "board", Board: toWireTasks(s.board)},
		{Kind: "gates", Gates: toWireGates(s.gates)},
		{Kind: "status", On: s.sphragisOn, Up: s.gatewayUp},
	}
}
