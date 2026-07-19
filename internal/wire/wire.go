// SPDX-License-Identifier: Apache-2.0

// Package wire is the deck's attach protocol: length-prefixed frames carrying
// raw pane output and JSON events on the per-session UI socket. The server
// stays byte-dumb; the TUI and any other client share this package.
package wire

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

// Proto is bumped on any breaking change to the attach protocol; no negotiation, match or refuse.
const Proto = 1

// MaxFrame bounds one frame so a corrupt length prefix cannot allocate unbounded memory.
const MaxFrame = 4 << 20

// Frame kinds on the UI socket: raw pane output vs JSON events.
const (
	KindOutput byte = 'o'
	KindEvent  byte = 'e'
)

// Event is the single JSON envelope for everything that is not pane output.
type Event struct {
	Kind string `json:"kind"`
	// hello (client -> server)
	Proto   int    `json:"proto,omitempty"`
	Version string `json:"version,omitempty"`
	// welcome (server -> client)
	Cfg      *config.Config `json:"cfg,omitempty"`
	Roster   []Role         `json:"roster,omitempty"`
	Board    []Task         `json:"board,omitempty"`
	Gates    []Gate         `json:"gates,omitempty"`
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

// Role mirrors one roster entry for the client.
type Role struct {
	Role       config.Role `json:"role"`
	Exited     bool        `json:"exited"`
	Gone       bool        `json:"gone"`
	Waiting    bool        `json:"waiting"`
	Paused     bool        `json:"paused,omitempty"`
	OverBudget bool        `json:"over_budget,omitempty"`
	Restarts   int         `json:"restarts"`
}

// Task mirrors one task-board event.
type Task struct {
	At     int64  `json:"at"` // unix nanos
	Kind   string `json:"k"`
	ID     string `json:"id,omitempty"`
	To     string `json:"to,omitempty"`
	Task   string `json:"t,omitempty"`
	File   string `json:"f,omitempty"`
	Done   bool   `json:"d,omitempty"`
	DoneAt int64  `json:"da,omitempty"`
	Round  int    `json:"r,omitempty"`  // judge loop round; 0 = unjudged
	Score  string `json:"sc,omitempty"` // judge verdict once parsed, e.g. "6/10"
}

// Gate mirrors one pending approval gate.
type Gate struct {
	Cmd    ipc.Command `json:"cmd"`
	To     string      `json:"to"`
	At     int64       `json:"at"`
	Reason string      `json:"reason,omitempty"` // judge fallback gates: approve accepts, reject revises
	Report string      `json:"report,omitempty"` // last report attached to a fallback gate
}

// Conn frames messages on the UI socket: [uint32 len][kind byte][payload].
type Conn struct {
	c  net.Conn
	wr sync.Mutex
}

func NewConn(c net.Conn) *Conn { return &Conn{c: c} }

func (w *Conn) writeFrame(kind byte, payload ...[]byte) error {
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
func (w *Conn) WriteOutput(idx int, chunk []byte) error {
	return w.writeFrame(KindOutput, []byte{byte(idx)}, chunk) // #nosec G115 -- one-byte pane index by frame contract; rosters stay far below 255
}

// WriteEvent ships one JSON event.
func (w *Conn) WriteEvent(ev Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return w.writeFrame(KindEvent, b)
}

// Read returns the next frame: for output, idx and chunk; for events, the decoded event.
func (w *Conn) Read() (kind byte, idx int, chunk []byte, ev Event, err error) {
	var hdr [5]byte
	if _, err = io.ReadFull(w.c, hdr[:]); err != nil {
		return 0, 0, nil, Event{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n < 1 || n > MaxFrame {
		return 0, 0, nil, Event{}, fmt.Errorf("wire: bad frame length %d", n)
	}
	kind = hdr[4]
	payload := make([]byte, n-1)
	if _, err = io.ReadFull(w.c, payload); err != nil {
		return 0, 0, nil, Event{}, err
	}
	switch kind {
	case KindOutput:
		if len(payload) < 1 {
			return 0, 0, nil, Event{}, fmt.Errorf("wire: empty output frame")
		}
		return kind, int(payload[0]), payload[1:], Event{}, nil
	case KindEvent:
		if err = json.Unmarshal(payload, &ev); err != nil {
			return 0, 0, nil, Event{}, fmt.Errorf("wire: bad event: %w", err)
		}
		return kind, 0, nil, ev, nil
	default:
		return 0, 0, nil, Event{}, fmt.Errorf("wire: unknown frame kind %q", kind)
	}
}

func (w *Conn) Close() error { return w.c.Close() }
