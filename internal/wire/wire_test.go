// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestConnRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	srv, cli := NewConn(a), NewConn(b)
	go func() {
		_ = srv.WriteOutput(3, []byte("hello"))
		_ = srv.WriteEvent(Event{Kind: "status", On: true, Up: true})
	}()
	kind, idx, chunk, _, err := cli.Read()
	if err != nil || kind != KindOutput || idx != 3 || string(chunk) != "hello" {
		t.Fatalf("output frame = %q idx=%d chunk=%q err=%v", kind, idx, chunk, err)
	}
	kind, _, _, ev, err := cli.Read()
	if err != nil || kind != KindEvent || ev.Kind != "status" || !ev.On || !ev.Up {
		t.Fatalf("event frame = %q ev=%+v err=%v", kind, ev, err)
	}
}

// rawFrame ships one hand-built frame so malformed inputs can be tested.
func rawFrame(t *testing.T, w net.Conn, length uint32, kind byte, payload []byte) {
	t.Helper()
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], length)
	hdr[4] = kind
	go func() {
		_, _ = w.Write(hdr[:])
		_, _ = w.Write(payload)
	}()
}

func TestConnRejectsBadFrames(t *testing.T) {
	cases := []struct {
		name    string
		length  uint32
		kind    byte
		payload []byte
	}{
		{"oversized length", MaxFrame + 1, KindEvent, nil},
		{"zero length", 0, KindEvent, nil},
		{"unknown kind", 2, 'x', []byte("z")},
		{"empty output frame", 1, KindOutput, nil},
		{"bad event json", 2, KindEvent, []byte("{")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			rawFrame(t, a, tc.length, tc.kind, tc.payload)
			if _, _, _, _, err := NewConn(b).Read(); err == nil {
				t.Fatal("Read accepted a malformed frame")
			}
		})
	}
}

// fakeServer accepts one connection, answers the hello with the scripted events, and closes.
func fakeServer(t *testing.T, script func(*Conn)) string {
	t.Helper()
	// SHORT socket path: macOS caps sun_path near 104 bytes
	short, err := os.MkdirTemp("/tmp", "cgw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	path := filepath.Join(short, "ui.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sc := NewConn(conn)
		if _, _, _, hello, err := sc.Read(); err != nil || hello.Kind != "hello" {
			_ = sc.Close()
			return
		}
		script(sc)
		_ = sc.Close()
	}()
	return path
}

func TestDialReplayPump(t *testing.T) {
	path := fakeServer(t, func(sc *Conn) {
		_ = sc.WriteEvent(Event{Kind: "welcome", Version: "v1"})
		_ = sc.WriteOutput(0, []byte("ring"))
		_ = sc.WriteEvent(Event{Kind: "ready"})
		_ = sc.WriteOutput(1, []byte("live"))
		_ = sc.WriteEvent(Event{Kind: "bell"})
	})
	c, welcome, err := Dial(path, "v1")
	if err != nil || welcome.Kind != "welcome" || welcome.Version != "v1" {
		t.Fatalf("Dial = %+v err=%v", welcome, err)
	}
	defer c.Close()
	var replay []byte
	if err := c.Replay(func(idx int, chunk []byte) { replay = append(replay, chunk...) }); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if string(replay) != "ring" {
		t.Fatalf("replay = %q, want ring", replay)
	}
	var live []byte
	var events []string
	err = c.Pump(
		func(idx int, chunk []byte) { live = append(live, chunk...) },
		func(ev Event) { events = append(events, ev.Kind) })
	if err == nil {
		t.Fatal("Pump returned nil after the server closed")
	}
	if string(live) != "live" || len(events) != 1 || events[0] != "bell" {
		t.Fatalf("pump saw live=%q events=%v", live, events)
	}
}

func TestDialRefusals(t *testing.T) {
	busyPath := fakeServer(t, func(sc *Conn) {
		_ = sc.WriteEvent(Event{Kind: "busy", PID: 42})
	})
	_, _, err := Dial(busyPath, "v1")
	var be *BusyError
	if !errors.As(err, &be) || be.PID != 42 {
		t.Fatalf("busy err = %v", err)
	}

	skewPath := fakeServer(t, func(sc *Conn) {
		_ = sc.WriteEvent(Event{Kind: "mismatch", Version: "v2"})
	})
	_, _, err = Dial(skewPath, "v1")
	var me *MismatchError
	if !errors.As(err, &me) || me.Server != "v2" || me.Client != "v1" {
		t.Fatalf("mismatch err = %v", err)
	}

	if _, _, err := Dial(filepath.Join(t.TempDir(), "missing.sock"), "v1"); err == nil {
		t.Fatal("Dial to a missing socket succeeded")
	}
}
