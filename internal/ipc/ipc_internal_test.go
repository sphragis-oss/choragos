// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func shortSocket(t *testing.T) string {
	t.Helper()
	// keep under the macOS sun_path limit; t.TempDir can exceed it
	return filepath.Join("/tmp", "chg-"+t.Name()[len(t.Name())-8:]+".sock")
}

func withTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	ioTimeoutNs.Store(int64(d))
	t.Cleanup(func() { ioTimeoutNs.Store(0) })
}

func TestServeConnSilentClientTimesOut(t *testing.T) {
	withTimeout(t, 200*time.Millisecond)
	path := shortSocket(t)
	got := make(chan Command, 1)
	srv, err := Serve(path, func(c Command) { got <- c })
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// send nothing; the server must close the connection on its deadline
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected server to close the silent connection")
	}
	select {
	case c := <-got:
		t.Fatalf("handler ran for a silent client: %+v", c)
	default:
	}

	// the server must still serve a valid command afterwards
	if err := Send(path, Command{Cmd: "delegate", To: []string{"coder"}, Task: "x"}); err != nil {
		t.Fatalf("send after timeout: %v", err)
	}
	select {
	case c := <-got:
		if c.Cmd != "delegate" {
			t.Fatalf("unexpected command: %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("valid command not handled after a silent client")
	}
}

func TestSendReturnsAgainstUnresponsiveServer(t *testing.T) {
	withTimeout(t, 200*time.Millisecond)
	path := shortSocket(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		time.Sleep(3 * time.Second) // hold the conn, never read or ack
		_ = conn.Close()
	}()

	start := time.Now()
	_ = Send(path, Command{Cmd: "work-done", Task: "x"}) // ack is best-effort; hanging is the failure
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Send hung for %v against an unresponsive server", d)
	}
}
