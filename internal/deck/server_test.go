// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/wire"
)

const serverTestVersion = "test-proto"

// startTestServer runs a headless session for a 2-role team and returns after its socket is up.
func startTestServer(t *testing.T) (done chan error) {
	t.Helper()
	t.Chdir(t.TempDir())
	// isolate SessionDir with a SHORT path: macOS caps sun_path near 104 bytes
	short, err := os.MkdirTemp("/tmp", "cg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	t.Setenv("XDG_RUNTIME_DIR", short)
	body := `[sphragis]
enabled = false

[[roles]]
name = "orchestrator"
command = "sh"
args = ["-c", "printf orch-banner; exec cat"]
start = true

[[roles]]
name = "reviewer"
command = "sh"
args = ["-c", "printf rev-banner; exec cat"]
approve = true
`
	if err := os.WriteFile("team.toml", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("team.toml")
	if err != nil {
		t.Fatal(err)
	}
	done = make(chan error, 1)
	go func() { done <- RunServer(cfg, serverTestVersion) }()
	if !waitFor(func() bool {
		_, err := os.Stat(ipc.UISocketPath())
		return err == nil
	}) {
		select {
		case err := <-done:
			t.Fatalf("server exited early: %v", err)
		default:
			t.Fatal("ui socket never appeared")
		}
	}
	t.Cleanup(func() {
		_ = ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "shutdown"})
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("server did not shut down")
		}
	})
	return done
}

// dialUI connects and completes the hello handshake, returning the first response event.
func dialUI(t *testing.T, version string) (*wire.Conn, wire.Event) {
	t.Helper()
	conn, err := net.Dial("unix", ipc.UISocketPath())
	if err != nil {
		t.Fatal(err)
	}
	wc := wire.NewConn(conn)
	if err := wc.WriteEvent(wire.Event{Kind: "hello", Proto: wire.Proto, Version: version}); err != nil {
		t.Fatal(err)
	}
	_, _, _, ev, err := wc.Read()
	if err != nil {
		t.Fatal(err)
	}
	return wc, ev
}

// readUntil consumes frames until an event of the wanted kind arrives, collecting pane output per idx.
func readUntil(t *testing.T, wc *wire.Conn, kind string, out map[int][]byte) wire.Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		k, idx, chunk, ev, err := wc.Read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if k == wire.KindOutput {
			if out != nil {
				out[idx] = append(out[idx], chunk...)
			}
			continue
		}
		if ev.Kind == kind {
			return ev
		}
	}
	t.Fatalf("event %q never arrived", kind)
	return wire.Event{}
}

// waitOutput reads frames until out[idx] contains substr, accumulating all pane output on the way.
func waitOutput(t *testing.T, wc *wire.Conn, out map[int][]byte, idx int, substr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(string(out[idx]), substr) {
		if !time.Now().Before(deadline) {
			t.Fatalf("output %q never arrived on idx %d: %q", substr, idx, out[idx])
		}
		k, i, chunk, _, err := wc.Read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if k == wire.KindOutput {
			out[i] = append(out[i], chunk...)
		}
	}
}

func TestServerAttachLifecycle(t *testing.T) {
	startTestServer(t)

	// version skew is refused with the server's version in the reply
	wc, ev := dialUI(t, "other-version")
	if ev.Kind != "mismatch" || ev.Version != serverTestVersion {
		t.Fatalf("skew handshake = %+v", ev)
	}
	_ = wc.Close()

	// a matching client gets the full welcome and the ring replay
	wc, ev = dialUI(t, serverTestVersion)
	if ev.Kind != "welcome" || ev.Cfg == nil || len(ev.Roster) != 2 {
		t.Fatalf("welcome = %+v", ev)
	}
	if ev.Roster[1].Role.Name != "reviewer" || !ev.Roster[1].Role.Approve {
		t.Fatalf("roster[1] = %+v", ev.Roster[1])
	}
	out := map[int][]byte{}
	readUntil(t, wc, "ready", out)

	// a second client is refused while the first is attached
	wc2, ev2 := dialUI(t, serverTestVersion)
	if ev2.Kind != "busy" || ev2.PID == 0 {
		t.Fatalf("second attach = %+v", ev2)
	}
	_ = wc2.Close()

	// the banner is delivered exactly once: in the ring replay or as an early live frame
	waitOutput(t, wc, out, 0, "orch-banner")

	// input round-trip: keystrokes reach the real PTY, the echo streams back
	if err := wc.WriteEvent(wire.Event{Kind: "input", Idx: 0, Data: []byte("marco\r")}); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, wc, out, 0, "marco")

	// a gated delegation raises a gates event; approving over the wire delivers it
	if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "delegate", To: []string{"reviewer"}, Task: "T-REMOTE"}); err != nil {
		t.Fatal(err)
	}
	gev := readUntil(t, wc, "gates", nil)
	if len(gev.Gates) != 1 || gev.Gates[0].To != "reviewer" {
		t.Fatalf("gates = %+v", gev.Gates)
	}
	if err := wc.WriteEvent(wire.Event{Kind: "gate", Approve: true}); err != nil {
		t.Fatal(err)
	}
	bev := readUntil(t, wc, "board", nil)
	if len(bev.Board) != 1 || bev.Board[0].To != "reviewer" || bev.Board[0].ID != "T1" {
		t.Fatalf("board = %+v", bev.Board)
	}

	// a wire restart replaces the pane: a reset arrives first, then only the fresh boot output
	if err := wc.WriteEvent(wire.Event{Kind: "restart", Idx: 0}); err != nil {
		t.Fatal(err)
	}
	rev := readUntil(t, wc, "reset", out)
	if rev.Idx != 0 {
		t.Fatalf("reset idx = %d, want 0", rev.Idx)
	}
	out[0] = nil
	waitOutput(t, wc, out, 0, "orch-banner")
	if strings.Contains(string(out[0]), "marco") {
		t.Fatalf("stale pre-restart output after reset: %q", out[0])
	}

	// detach leaves the session running; re-attach returns the checkpointed layout
	layout := []byte(`{"root":{"leaf":true,"role":1},"focused":1}`)
	if err := wc.WriteEvent(wire.Event{Kind: "layout", Data: layout}); err != nil {
		t.Fatal(err)
	}
	if err := wc.WriteEvent(wire.Event{Kind: "detach"}); err != nil {
		t.Fatal(err)
	}
	// keep wc open: closing now can fail a server write and drop the client before layout lands
	var w3 wire.Event
	var wc3 *wire.Conn
	if !waitFor(func() bool {
		wc3, w3 = dialUI(t, serverTestVersion)
		if w3.Kind == "welcome" {
			return true
		}
		_ = wc3.Close() // server may not have processed the detach yet
		return false
	}) {
		t.Fatalf("re-attach failed: %+v", w3)
	}
	_ = wc.Close()
	if string(w3.Layout) != string(layout) {
		t.Fatalf("layout = %s, want %s", w3.Layout, layout)
	}
	readUntil(t, wc3, "ready", nil)
	_ = wc3.WriteEvent(wire.Event{Kind: "detach"})
	_ = wc3.Close()
}
