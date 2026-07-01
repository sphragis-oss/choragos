// SPDX-License-Identifier: Apache-2.0

package ipc_test

import (
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
