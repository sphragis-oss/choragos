// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
	"github.com/sphragis-oss/choragos/internal/sphragis"
)

// outputMsg carries one teed pane chunk into the server loop for wire forwarding.
type outputMsg struct {
	idx   int
	p     *pane.Pane // identifies the source; chunks from a replaced pane are dropped
	chunk []byte
	seq   uint64
}

// attachMsg hands an accepted UI connection (hello already read) to the loop.
type attachMsg struct {
	wc    *wireConn
	hello wireEvent
}

// clientMsg carries one event from the attached client into the loop.
type clientMsg struct{ ev wireEvent }

// clientGoneMsg reports the attached client's reader exiting.
type clientGoneMsg struct{ wc *wireConn }

// server runs a session headless: the deck brain without a terminal, one attachable client.
type server struct {
	sess    *session
	msgs    chan any
	version string
	client  *wireConn
	snap    map[int]uint64 // per-role ring sequence at attach; older chunks are already replayed
	layout  []byte         // client wm checkpoint, restored on the next attach
	wired   map[int]*pane.Pane
}

// RunServer runs the session core headless until shutdown; `choragos attach` brings the UI.
func RunServer(cfg config.Config, version string) error {
	s := &session{cfg: cfg}
	msgs := make(chan any, 1024)
	s.notify = func(v any) { msgs <- v }
	srv := &server{sess: s, msgs: msgs, version: version, wired: map[int]*pane.Pane{}}
	s.focusFn = func(i int) { srv.sendEvent(wireEvent{Kind: "focus", Idx: i}) }
	s.bellFn = func() { srv.sendEvent(wireEvent{Kind: "bell"}) }
	if err := s.start(80, 24); err != nil {
		return err
	}
	defer s.closeAll()
	ipc.WriteMeta(s.socket)
	defer ipc.RemoveMeta()
	srv.ensureTees()

	uiPath := ipc.UISocketPath()
	_ = os.Remove(uiPath)
	ln, err := net.Listen("unix", uiPath)
	if err != nil {
		return fmt.Errorf("ui socket: %w", err)
	}
	_ = os.Chmod(uiPath, 0o600)
	defer func() { _ = ln.Close(); _ = os.Remove(uiPath) }()
	go srv.accept(ln)

	if s.sphragisOn {
		go func() {
			sup, err := sphragis.Ensure(cfg.Sphragis)
			msgs <- gatewayReadyMsg{sup: sup, err: err}
		}()
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	s.log().Info("server ready", "socket", s.socket, "ui", uiPath)

	for {
		select {
		case <-sigCh:
			return nil
		case <-ticker.C:
			s.bootPanes()
			s.checkWaiting()
			if s.sphragisOn {
				go func() { msgs <- gatewayHealthMsg{up: sphragis.Healthy(cfg.Sphragis.Addr)} }()
			}
			srv.syncClient()
		case v := <-msgs:
			if quit := srv.handle(v); quit {
				return nil
			}
		}
	}
}

// handle applies one loop message; true means shut down.
func (srv *server) handle(v any) bool {
	s := srv.sess
	switch msg := v.(type) {
	case frameMsg:
		if msg.idx >= 0 && msg.idx < len(s.panes) && s.panes[msg.idx].gen == msg.gen {
			s.panes[msg.idx].lastActive = time.Now()
		}
	case outputMsg:
		if srv.client != nil && srv.wired[msg.idx] == msg.p && msg.seq > srv.snap[msg.idx] {
			if err := srv.client.WriteOutput(msg.idx, msg.chunk); err != nil {
				srv.dropClient()
			}
		}
	case paneClosedMsg:
		if msg.idx >= 0 && msg.idx < len(s.panes) && s.panes[msg.idx].gen == msg.gen {
			s.panes[msg.idx].exited = true
			s.log().Warn("pane exited", "role", s.panes[msg.idx].role.Name)
			s.autoRestart(s.panes[msg.idx], msg.idx)
			srv.ensureTees()
			srv.syncClient()
		}
	case ipcMsg:
		switch msg.cmd.Cmd {
		case "shutdown":
			return true
		case "reload":
			cw, ch := s.panes[s.startIdx()].pane.Size()
			s.reload(cw, ch)
			srv.ensureTees()
		default:
			s.dispatch(msg.cmd)
		}
		srv.syncClient()
	case gatewayReadyMsg:
		if msg.err == nil {
			s.gateway = msg.sup
			s.gatewayUp = true
			s.log().Info("gateway ready", "addr", s.cfg.Sphragis.Addr)
		} else {
			s.log().Error("gateway start failed", "err", msg.err)
		}
	case gatewayHealthMsg:
		if msg.up != s.gatewayUp {
			s.log().Warn("gateway health changed", "up", msg.up)
		}
		s.gatewayUp = msg.up
	case attachMsg:
		srv.attach(msg.wc, msg.hello)
	case clientGoneMsg:
		if srv.client == msg.wc {
			srv.dropClient()
		}
	case clientMsg:
		return srv.handleClient(msg.ev)
	}
	return false
}

// handleClient applies one event from the attached client; true means shut down.
func (srv *server) handleClient(ev wireEvent) bool {
	s := srv.sess
	switch ev.Kind {
	case "input":
		if ev.Idx >= 0 && ev.Idx < len(s.panes) && !s.panes[ev.Idx].exited {
			_ = s.panes[ev.Idx].pane.Input(ev.Data)
		}
	case "resize":
		if ev.Idx >= 0 && ev.Idx < len(s.panes) && !s.panes[ev.Idx].exited {
			_ = s.panes[ev.Idx].pane.Resize(ev.Cols, ev.Rows)
		}
	case "gate":
		if ev.Approve {
			s.approveGate()
		} else {
			s.rejectGate()
		}
		srv.syncClient()
	case "restart":
		if ev.Idx >= 0 && ev.Idx < len(s.panes) && !s.panes[ev.Idx].gone {
			e := s.panes[ev.Idx]
			cw, ch := e.pane.Size()
			s.restart(e, ev.Idx, cw, ch)
			srv.ensureTees()
			srv.syncClient()
		}
	case "reload":
		cw, ch := s.panes[s.startIdx()].pane.Size()
		s.reload(cw, ch)
		srv.ensureTees()
		srv.syncClient()
	case "sphragis":
		s.sphragisOn = !s.sphragisOn
		srv.syncClient()
	case "layout":
		srv.layout = ev.Data
	case "detach":
		s.log().Info("client detached")
		srv.dropClient()
	case "quit":
		s.log().Info("client requested shutdown")
		return true
	}
	return false
}

// accept reads each connection's hello off the loop, then hands it over for attach.
func (srv *server) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			wc := newWireConn(conn)
			_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
			kind, _, _, ev, err := wc.Read()
			if err != nil || kind != kindEvent || ev.Kind != "hello" {
				_ = wc.Close()
				return
			}
			_ = conn.SetReadDeadline(time.Time{})
			srv.msgs <- attachMsg{wc: wc, hello: ev}
		}()
	}
}

// attach admits one client: handshake checks, full state, ring replay, then live streaming.
func (srv *server) attach(wc *wireConn, hello wireEvent) {
	s := srv.sess
	if srv.client != nil {
		_ = wc.WriteEvent(wireEvent{Kind: "busy", PID: os.Getpid()})
		_ = wc.Close()
		return
	}
	if hello.Proto != wireProto || hello.Version != srv.version {
		_ = wc.WriteEvent(wireEvent{Kind: "mismatch", Proto: wireProto, Version: srv.version})
		_ = wc.Close()
		return
	}
	snap := make(map[int]uint64, len(s.panes))
	rings := make([][]byte, len(s.panes))
	for i, e := range s.panes {
		rings[i], snap[i] = e.pane.RingBytes()
	}
	welcome := wireEvent{Kind: "welcome", Proto: wireProto, Version: srv.version, Cfg: &s.cfg, Layout: srv.layout}
	state := s.snapshotEvents()
	welcome.Roster, welcome.Board, welcome.Gates = state[0].Roster, state[1].Board, state[2].Gates
	if err := wc.WriteEvent(welcome); err != nil {
		_ = wc.Close()
		return
	}
	for i, b := range rings {
		if len(b) > 0 {
			if err := wc.WriteOutput(i, b); err != nil {
				_ = wc.Close()
				return
			}
		}
	}
	if err := wc.WriteEvent(wireEvent{Kind: "ready"}); err != nil {
		_ = wc.Close()
		return
	}
	srv.client, srv.snap = wc, snap
	s.log().Info("client attached")
	go func() {
		for {
			kind, _, _, ev, err := wc.Read()
			if err != nil {
				srv.msgs <- clientGoneMsg{wc: wc}
				return
			}
			if kind == kindEvent {
				srv.msgs <- clientMsg{ev: ev}
				if ev.Kind == "detach" || ev.Kind == "quit" {
					return
				}
			}
		}
	}()
}

// dropClient severs the attached client; the session keeps running.
func (srv *server) dropClient() {
	if srv.client != nil {
		_ = srv.client.Close()
		srv.client = nil
	}
}

// sendEvent ships one event to the attached client, dropping it on write failure.
func (srv *server) sendEvent(ev wireEvent) {
	if srv.client == nil {
		return
	}
	if err := srv.client.WriteEvent(ev); err != nil {
		srv.dropClient()
	}
}

// syncClient pushes the full state snapshot (roster, board, gates, status) to the client.
func (srv *server) syncClient() {
	if srv.client == nil {
		return
	}
	for _, ev := range srv.sess.snapshotEvents() {
		if srv.client == nil {
			return
		}
		srv.sendEvent(ev)
	}
}

// ensureTees keeps every live pane teed into the loop; respawns swap the pane object.
func (srv *server) ensureTees() {
	for i, e := range srv.sess.panes {
		if e.gone || srv.wired[i] == e.pane {
			continue
		}
		idx, p, swapped := i, e.pane, srv.wired[i] != nil
		p.SetTee(func(chunk []byte, seq uint64) {
			cp := append([]byte(nil), chunk...) // the stream loop reuses its buffer
			srv.msgs <- outputMsg{idx: idx, p: p, chunk: cp, seq: seq}
		})
		srv.wired[i] = p
		if swapped {
			srv.resetClientPane(idx, p)
		}
	}
}

// resetClientPane tells the attached client the pane was replaced, replaying its fresh ring.
func (srv *server) resetClientPane(idx int, p *pane.Pane) {
	if srv.client == nil {
		return
	}
	srv.sendEvent(wireEvent{Kind: "reset", Idx: idx})
	b, seq := p.RingBytes()
	srv.snap[idx] = seq
	if len(b) > 0 && srv.client != nil {
		if err := srv.client.WriteOutput(idx, b); err != nil {
			srv.dropClient()
		}
	}
}
