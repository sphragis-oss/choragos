// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/wire"
)

func TestClientByeQuitsCleanly(t *testing.T) {
	m := newTestModel([]*entry{})
	_, cmd := m.Update(remoteEvMsg{ev: wire.Event{Kind: "bye", Reason: "handoff"}})
	if cmd == nil {
		t.Fatal("bye must quit the client")
	}
	if !strings.Contains(m.bye, "serve --resume") {
		t.Fatalf("handoff goodbye must carry the resume hint: %q", m.bye)
	}
	_, cmd = m.Update(connLostMsg{err: errors.New("EOF")})
	if cmd == nil || m.err != nil {
		t.Fatalf("conn loss after a bye is expected, not an error: %v", m.err)
	}

	m2 := newTestModel([]*entry{})
	m2.Update(connLostMsg{err: errors.New("EOF")})
	if m2.err == nil {
		t.Fatal("conn loss without a bye must stay an error")
	}
	if !strings.Contains(byeMessage("shutdown"), "shutdown") {
		t.Fatalf("shutdown goodbye = %q", byeMessage("shutdown"))
	}
}

func TestServerSaysByeOnHandoff(t *testing.T) {
	done := startTestServer(t)
	wc, welcome := dialUI(t, serverTestVersion)
	defer func() { _ = wc.Close() }()
	if welcome.Kind != "welcome" {
		t.Fatalf("handshake = %+v", welcome)
	}
	readUntil(t, wc, "ready", map[int][]byte{})
	if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "handoff"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // the document must be younger than the request
	if err := os.WriteFile(filepath.Join(contextDir, handoffFile), []byte("# handoff"), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := readUntil(t, wc, "bye", map[int][]byte{})
	if ev.Reason != "handoff" {
		t.Fatalf("bye reason = %q, want handoff", ev.Reason)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exit: %v", err)
		}
		done <- nil // the harness cleanup waits on done too
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop after the handoff")
	}
}

func TestServerSaysByeOnShutdown(t *testing.T) {
	done := startTestServer(t)
	wc, welcome := dialUI(t, serverTestVersion)
	defer func() { _ = wc.Close() }()
	if welcome.Kind != "welcome" {
		t.Fatalf("handshake = %+v", welcome)
	}
	readUntil(t, wc, "ready", map[int][]byte{})
	if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	if ev := readUntil(t, wc, "bye", map[int][]byte{}); ev.Reason != "shutdown" {
		t.Fatalf("bye reason = %q, want shutdown", ev.Reason)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exit: %v", err)
		}
		done <- nil
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop after the shutdown")
	}
}
