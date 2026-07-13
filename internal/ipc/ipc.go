// SPDX-License-Identifier: Apache-2.0

// Package ipc is the JSON-over-unix-socket control channel between the deck and the delegate/work-done verbs.
package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// ioTimeoutNs overrides the IPC exchange bound in nanoseconds; zero means the default. Tests shrink it.
var ioTimeoutNs atomic.Int64

// ioTimeout bounds a whole IPC exchange so a silent or wedged peer can never park a goroutine or hang the CLI.
func ioTimeout() time.Duration {
	if d := ioTimeoutNs.Load(); d > 0 {
		return time.Duration(d)
	}
	return 5 * time.Second
}

// EnvSocket is the env var pointing a worker CLI at its deck's control socket.
const EnvSocket = "CHORAGOS_SOCK"

// Command is one control message.
type Command struct {
	Cmd    string   `json:"cmd"` // "delegate", "work-done", or "reload"
	To     []string `json:"to,omitempty"`
	Task   string   `json:"task,omitempty"`
	Brief  string   `json:"brief,omitempty"`  // absolute path to a delegation brief file
	Report string   `json:"report,omitempty"` // absolute path to a work-done report file
	Done   bool     `json:"done,omitempty"`
	ID     string   `json:"id,omitempty"` // task id assigned by the deck on delegate, echoed by work-done
}

// SocketPath resolves the control socket: $CHORAGOS_SOCK, else a per-user runtime/temp path.
func SocketPath() string {
	if p := os.Getenv(EnvSocket); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "choragos.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("choragos-%d.sock", os.Getuid()))
}

// Server accepts control commands and hands each to a callback.
type Server struct{ ln net.Listener }

// Serve listens on path (0600) and calls handle for every command received.
func Serve(path string, handle func(Command)) (*Server, error) {
	_ = os.Remove(path) // clear a stale socket from a crashed run
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	s := &Server{ln: ln}
	go s.accept(handle)
	return s, nil
}

func (s *Server) accept(handle func(Command)) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go serveConn(conn, handle)
	}
}

func serveConn(conn net.Conn, handle func(Command)) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(ioTimeout()))
	var cmd Command
	if err := json.NewDecoder(conn).Decode(&cmd); err != nil {
		return
	}
	handle(cmd)
	_ = json.NewEncoder(conn).Encode(ack{Status: "ok"})
}

type ack struct {
	Status string `json:"status"`
}

// Close stops the server.
func (s *Server) Close() error { return s.ln.Close() }

// Send delivers one command to the deck listening at path and waits for its ack.
func Send(path string, cmd Command) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(ioTimeout()))
	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		return err
	}
	var a ack
	_ = json.NewDecoder(conn).Decode(&a) // best-effort ack; command is already delivered
	return nil
}
