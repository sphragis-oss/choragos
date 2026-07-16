// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"fmt"
	"net"
	"time"
)

// HelloTimeout bounds the attach handshake so a wedged server cannot hang the client.
const HelloTimeout = 5 * time.Second

// BusyError reports another client already holds the session.
type BusyError struct{ PID int }

func (e *BusyError) Error() string {
	return fmt.Sprintf("a client is already attached (pid %d)", e.PID)
}

// MismatchError reports client/server version skew.
type MismatchError struct{ Server, Client string }

func (e *MismatchError) Error() string {
	return fmt.Sprintf("version mismatch: server runs %s, this client is %s; finish or kill the session, then restart it", e.Server, e.Client)
}

// Dial connects to a session's UI socket and completes the hello handshake,
// returning the welcome event. Refusals surface as BusyError or MismatchError.
func Dial(path, version string) (*Conn, Event, error) {
	nc, err := net.Dial("unix", path)
	if err != nil {
		return nil, Event{}, err
	}
	c := NewConn(nc)
	if err := c.WriteEvent(Event{Kind: "hello", Proto: Proto, Version: version}); err != nil {
		_ = c.Close()
		return nil, Event{}, err
	}
	_ = nc.SetReadDeadline(time.Now().Add(HelloTimeout))
	_, _, _, ev, err := c.Read()
	if err != nil {
		_ = c.Close()
		return nil, Event{}, fmt.Errorf("attach handshake: %w", err)
	}
	_ = nc.SetReadDeadline(time.Time{})
	switch ev.Kind {
	case "welcome":
		return c, ev, nil
	case "busy":
		_ = c.Close()
		return nil, Event{}, &BusyError{PID: ev.PID}
	case "mismatch":
		_ = c.Close()
		return nil, Event{}, &MismatchError{Server: ev.Version, Client: version}
	default:
		_ = c.Close()
		return nil, Event{}, fmt.Errorf("attach handshake: unexpected %q", ev.Kind)
	}
}

// Replay consumes the post-welcome ring replay, feeding output frames to
// onOutput until the server's ready event; each frame is deadline-bounded.
func (w *Conn) Replay(onOutput func(idx int, chunk []byte)) error {
	for {
		_ = w.c.SetReadDeadline(time.Now().Add(HelloTimeout))
		kind, idx, chunk, ev, err := w.Read()
		if err != nil {
			return err
		}
		if kind == KindEvent && ev.Kind == "ready" {
			_ = w.c.SetReadDeadline(time.Time{})
			return nil
		}
		if kind == KindOutput {
			onOutput(idx, chunk)
		}
	}
}

// Pump reads frames until the connection dies, invoking the callbacks in wire
// order; it returns the terminal read error.
func (w *Conn) Pump(onOutput func(idx int, chunk []byte), onEvent func(Event)) error {
	for {
		kind, idx, chunk, ev, err := w.Read()
		if err != nil {
			return err
		}
		switch kind {
		case KindOutput:
			onOutput(idx, chunk)
		case KindEvent:
			onEvent(ev)
		}
	}
}
