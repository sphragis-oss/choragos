// SPDX-License-Identifier: Apache-2.0

package ipc_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func TestRoundTripDeterministic(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "c.sock")
	var mu sync.Mutex
	var got []ipc.Command
	srv, err := ipc.Serve(sock, func(c ipc.Command) {
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Send blocks until the deck acks, which happens after the handler runs,
	// so every command is delivered by the time Send returns: 20/20, no drops.
	for i := 0; i < 20; i++ {
		if err := ipc.Send(sock, ipc.Command{Cmd: "delegate", To: []string{"coder"}, Task: "task"}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 20 {
		t.Fatalf("received %d/20 commands", n)
	}
	for i, c := range got {
		if c.Cmd != "delegate" || len(c.To) != 1 || c.To[0] != "coder" || c.Task != "task" {
			t.Fatalf("command %d corrupted: %+v", i, c)
		}
	}
}

func TestSendNoServer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "absent.sock")
	if err := ipc.Send(sock, ipc.Command{Cmd: "delegate"}); err == nil {
		t.Fatal("expected error dialing a missing socket")
	}
}

func TestSocketPathUsesEnv(t *testing.T) {
	t.Setenv(ipc.EnvSocket, "/tmp/custom.sock")
	if got := ipc.SocketPath(); got != "/tmp/custom.sock" {
		t.Fatalf("SocketPath = %q, want /tmp/custom.sock", got)
	}
}

func TestSessionPaths(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv(ipc.EnvSocket, "")
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	dir := ipc.SessionDir()
	if filepath.Dir(dir) != runtime {
		t.Fatalf("SessionDir = %q, want a child of %q", dir, runtime)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("SessionDir not created: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("SessionDir mode = %o, want 0700", fi.Mode().Perm())
	}

	id := ipc.SessionID(wd)
	if len(id) != 8 {
		t.Fatalf("SessionID length = %d, want 8", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("SessionID %q is not hex: %v", id, err)
	}
	if ipc.SessionID(wd) != id || ipc.SessionID("/elsewhere") == id {
		t.Error("SessionID must be deterministic per directory and differ across directories")
	}

	if got, want := ipc.SocketPath(), filepath.Join(dir, id+".sock"); got != want {
		t.Errorf("SocketPath = %q, want %q", got, want)
	}
	if got, want := ipc.UISocketPath(), filepath.Join(dir, id+".ui.sock"); got != want {
		t.Errorf("UISocketPath = %q, want %q", got, want)
	}
	if got, want := ipc.MetaPath(), filepath.Join(dir, id+".json"); got != want {
		t.Errorf("MetaPath = %q, want %q", got, want)
	}
}

func TestMetaLifecycle(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(ipc.EnvSocket, "")
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	ipc.WriteMeta("/tmp/x.sock")
	// junk sidecars must be skipped, not break the listing
	for name, body := range map[string]string{"junk.json": "{not json", "nosocket.json": "{}", "readme.txt": "hi"} {
		if err := os.WriteFile(filepath.Join(ipc.SessionDir(), name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	metas := ipc.ReadMetas()
	if len(metas) != 1 {
		t.Fatalf("ReadMetas = %+v, want exactly the one real session", metas)
	}
	m := metas[0]
	if m.PID != os.Getpid() || m.Dir != wd || m.Socket != "/tmp/x.sock" {
		t.Fatalf("meta = %+v, want pid %d dir %q socket /tmp/x.sock", m, os.Getpid(), wd)
	}

	ipc.RemoveMeta()
	if metas := ipc.ReadMetas(); len(metas) != 0 {
		t.Fatalf("ReadMetas after RemoveMeta = %+v, want none", metas)
	}
}

func TestServeReplacesStaleSocket(t *testing.T) {
	// short path: macOS caps sun_path near 104 bytes
	sock := filepath.Join("/tmp", "chg-stale.sock")
	t.Cleanup(func() { _ = os.Remove(sock) })
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := ipc.Serve(sock, func(ipc.Command) {})
	if err != nil {
		t.Fatalf("Serve over a stale socket: %v", err)
	}
	defer srv.Close()
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %o, want 0600", fi.Mode().Perm())
	}
	if err := ipc.Send(sock, ipc.Command{Cmd: "ping"}); err != nil {
		t.Fatalf("send after stale replacement: %v", err)
	}
}
