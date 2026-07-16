// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
	"github.com/sphragis-oss/choragos/internal/wire"
	"github.com/sphragis-oss/choragos/internal/wm"
)

// remoteEvMsg carries a server state event into the client's update loop.
type remoteEvMsg struct{ ev wire.Event }

// connLostMsg reports the attach connection dying; the client quits with the error.
type connLostMsg struct{ err error }

// RunAttach connects the TUI client to the detached session for this working directory.
func RunAttach(version string) error {
	wc, welcome, err := wire.Dial(ipc.UISocketPath(), version)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) {
			return fmt.Errorf("no session for this directory (start one with: choragos serve --detach)")
		}
		return err
	}

	m := newClientModel(wc, welcome)
	// consume the ring replay synchronously; frames after "ready" flow through the program
	if err := wc.Replay(func(idx int, chunk []byte) {
		if idx >= 0 && idx < len(m.panes) {
			m.panes[idx].pane.Feed(chunk)
		}
	}); err != nil {
		return fmt.Errorf("attach replay: %w", err)
	}

	m.prog = tea.NewProgram(m, programOptions(m.cfg)...)
	go clientReader(m, wc)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		_ = wc.WriteEvent(wire.Event{Kind: "detach"})
		m.prog.Kill()
	}()
	defer func() { _ = wc.Close() }()
	if _, err := m.prog.Run(); err != nil {
		return err
	}
	return m.err
}

// newClientModel builds the Model from a welcome event: remote panes, synced state, restored layout.
func newClientModel(wc *wire.Conn, ev wire.Event) *Model {
	cfg := config.Config{}
	if ev.Cfg != nil {
		cfg = *ev.Cfg
	}
	s := &session{cfg: cfg}
	s.panes = rosterEntries(wc, ev.Roster, nil)
	m := &Model{session: s, remote: wc, w: 160, h: 48}
	m.wireSession()
	m.keys = cfg.Keys.Defaulted()
	m.autoFocus = cfg.UI.IsAutoFocus()
	m.sidebar = cfg.UI.SidebarStart()
	if cfg.UI.IsBell() {
		m.bellFn = func() { _, _ = os.Stdout.WriteString("\a") }
	}
	s.board = fromWireTasks(ev.Board)
	s.gates = fromWireGates(ev.Gates)
	m.active = m.startIdx()
	m.tree = wm.New(m.active)
	if len(ev.Layout) > 0 {
		if t, err := wm.Unmarshal(ev.Layout); err == nil {
			m.tree = t
			m.active = t.FocusedRole()
		}
	}
	return m
}

// rosterEntries syncs wire roster rows into entries, appending remote panes for new roles.
func rosterEntries(wc *wire.Conn, roster []wire.Role, existing []*entry) []*entry {
	now := time.Now()
	out := existing
	for i, wr := range roster {
		if i < len(out) {
			e := out[i]
			e.role = wr.Role
			e.exited, e.gone, e.waiting, e.paused, e.restarts = wr.Exited, wr.Gone, wr.Waiting, wr.Paused, wr.Restarts
			continue
		}
		idx := i
		p := pane.Remote(80, 24,
			func(b []byte) error { return wc.WriteEvent(wire.Event{Kind: "input", Idx: idx, Data: b}) },
			func(cols, rows int) { _ = wc.WriteEvent(wire.Event{Kind: "resize", Idx: idx, Cols: cols, Rows: rows}) })
		out = append(out, &entry{
			role: wr.Role, pane: p, exited: wr.Exited, gone: wr.Gone, waiting: wr.Waiting,
			paused: wr.Paused, restarts: wr.Restarts, startedAt: now, lastActive: now,
		})
	}
	return out
}

// clientReader pumps wire frames into the running program until the connection dies.
func clientReader(m *Model, wc *wire.Conn) {
	err := wc.Pump(func(idx int, chunk []byte) {
		if idx >= 0 && idx < len(m.panes) {
			m.panes[idx].pane.Feed(chunk)
			m.prog.Send(frameMsg{idx: idx, gen: m.panes[idx].gen})
		}
	}, func(ev wire.Event) {
		// reset must land before the frames that follow it, so apply it here in wire order
		if ev.Kind == "reset" && ev.Idx >= 0 && ev.Idx < len(m.panes) {
			m.panes[ev.Idx].pane.Reset()
		}
		m.prog.Send(remoteEvMsg{ev: ev})
	})
	m.prog.Send(connLostMsg{err: err})
}

// applyRemoteEvent syncs server state events into the client model.
func (m *Model) applyRemoteEvent(ev wire.Event) {
	switch ev.Kind {
	case "roster":
		m.panes = rosterEntries(m.remote, ev.Roster, m.panes)
		for idx, e := range m.panes {
			if e.gone && m.tree != nil && m.tree.FocusRole(idx) {
				if !m.tree.Close() {
					m.tree.Focus(m.startIdx())
				}
				m.syncFocus()
			}
		}
	case "board":
		m.board = fromWireTasks(ev.Board)
	case "gates":
		m.gates = fromWireGates(ev.Gates)
	case "status":
		m.sphragisOn, m.gatewayUp = ev.On, ev.Up
	case "bell":
		if m.bellFn != nil {
			m.bellFn()
		}
	case "focus":
		if m.autoFocus && !m.manual {
			m.focusRole(ev.Idx)
		}
	}
}

// fromWireTasks restores the task board from the wire.
func fromWireTasks(in []wire.Task) []taskEvent {
	out := make([]taskEvent, 0, len(in))
	for _, w := range in {
		ev := taskEvent{at: time.Unix(0, w.At), kind: w.Kind, id: w.ID, to: w.To, task: w.Task, file: w.File, done: w.Done}
		if w.DoneAt != 0 {
			ev.doneAt = time.Unix(0, w.DoneAt)
		}
		out = append(out, ev)
	}
	return out
}

// fromWireGates restores the pending gates from the wire.
func fromWireGates(in []wire.Gate) []pendingGate {
	out := make([]pendingGate, 0, len(in))
	for _, w := range in {
		out = append(out, pendingGate{cmd: w.Cmd, to: w.To, at: time.Unix(0, w.At)})
	}
	return out
}
