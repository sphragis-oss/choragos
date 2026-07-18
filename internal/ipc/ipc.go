// SPDX-License-Identifier: Apache-2.0

// Package ipc is the JSON-over-unix-socket control channel between the deck and the delegate/work-done verbs.
package ipc

import (
	"crypto/sha256"
	"encoding/hex"
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

// SessionDir is the per-user directory holding session sockets and metadata (0700).
func SessionDir() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, fmt.Sprintf("choragos-%d", os.Getuid()))
	_ = os.MkdirAll(dir, 0o700) // #nosec G703 -- XDG_RUNTIME_DIR/TempDir plus a fixed uid-suffixed name
	return dir
}

// SessionID names the session for a working directory: an 8-hex dir hash,
// because unix socket paths cap near 104 bytes on macOS.
func SessionID(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(sum[:4])
}

// cwdID is the session id for the current working directory.
func cwdID() string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "unknown"
	}
	return SessionID(wd)
}

// SocketPath resolves the control socket: $CHORAGOS_SOCK, else the per-session
// (working-directory) path, so one deck per project instead of one per user.
func SocketPath() string {
	if p := os.Getenv(EnvSocket); p != "" {
		return p
	}
	return filepath.Join(SessionDir(), cwdID()+".sock")
}

// UISocketPath is the attach socket next to the control socket.
func UISocketPath() string {
	return filepath.Join(SessionDir(), cwdID()+".ui.sock")
}

// Meta describes a running session, written next to its sockets for `choragos ls`.
type Meta struct {
	PID     int       `json:"pid"`
	Dir     string    `json:"dir"`
	Started time.Time `json:"started"`
	Socket  string    `json:"socket"`
}

// MetaPath is the sidecar metadata file for the current working directory's session.
func MetaPath() string {
	return filepath.Join(SessionDir(), cwdID()+".json")
}

// WriteMeta records the running session; best-effort.
func WriteMeta(socket string) {
	wd, _ := os.Getwd()
	b, _ := json.Marshal(Meta{PID: os.Getpid(), Dir: wd, Started: time.Now(), Socket: socket})
	_ = os.WriteFile(MetaPath(), b, 0o600)
}

// ReadMetas lists every session sidecar in the session dir.
func ReadMetas() []Meta {
	ents, err := os.ReadDir(SessionDir())
	if err != nil {
		return nil
	}
	var out []Meta
	for _, e := range ents {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(SessionDir(), e.Name()))
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) == nil && m.Socket != "" {
			out = append(out, m)
		}
	}
	return out
}

// RemoveMeta drops the sidecar for the current working directory's session.
func RemoveMeta() { _ = os.Remove(MetaPath()) }

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
