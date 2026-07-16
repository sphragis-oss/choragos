// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/wire"
)

// App is the Wails-bound backend: session discovery plus one read-only attach.
type App struct {
	ctx     context.Context
	version string

	mu   sync.Mutex
	conn *wire.Conn
	gen  int // bumped per attach; a stale pump's events are dropped
}

func newApp(version string) *App { return &App{version: version} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

func (a *App) shutdown(context.Context) { a.Detach() }

// Session is one running session row for the picker.
type Session struct {
	ID   string `json:"id"`
	Dir  string `json:"dir"`
	Name string `json:"name"`
	PID  int    `json:"pid"`
	Up   string `json:"up"`
}

// AutoAttachDir is a dev/test hook: a directory to attach to on startup.
func (a *App) AutoAttachDir() string { return os.Getenv("CHORAGOS_DESKTOP_AUTOATTACH") }

// Sessions lists the live sessions, pinging each sidecar like `choragos ls`.
func (a *App) Sessions() []Session {
	var out []Session
	for _, m := range ipc.ReadMetas() {
		if ipc.Send(m.Socket, ipc.Command{Cmd: "ping"}) != nil {
			continue // stale sidecar; `choragos ls` prunes it
		}
		out = append(out, Session{
			ID:   ipc.SessionID(m.Dir),
			Dir:  m.Dir,
			Name: filepath.Base(m.Dir),
			PID:  m.PID,
			Up:   time.Since(m.Started).Round(time.Second).String(),
		})
	}
	return out
}

// Roster is the welcome roster handed to the frontend.
type Roster struct {
	Roles []Role `json:"roles"`
}

// Role mirrors one wire roster row with just what the cards render.
type Role struct {
	Name    string `json:"name"`
	Start   bool   `json:"start"`
	Model   string `json:"model"`
	Exited  bool   `json:"exited"`
	Gone    bool   `json:"gone"`
	Waiting bool   `json:"waiting"`
	Paused  bool   `json:"paused"`
}

func toRoles(in []wire.Role) []Role {
	out := make([]Role, 0, len(in))
	for _, r := range in {
		out = append(out, Role{
			Name: r.Role.Name, Start: r.Role.Start, Model: r.Role.Model,
			Exited: r.Exited, Gone: r.Gone, Waiting: r.Waiting, Paused: r.Paused,
		})
	}
	return out
}

// Task mirrors one board row; At/DoneAt are unix millis for JS dates.
type Task struct {
	At     int64  `json:"at"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	To     string `json:"to"`
	Task   string `json:"task"`
	File   string `json:"file"`
	Done   bool   `json:"done"`
	DoneAt int64  `json:"doneAt"`
}

func toTasks(in []wire.Task) []Task {
	out := make([]Task, 0, len(in))
	for _, t := range in {
		out = append(out, Task{
			At: t.At / 1e6, Kind: t.Kind, ID: t.ID, To: t.To,
			Task: t.Task, File: t.File, Done: t.Done, DoneAt: t.DoneAt / 1e6,
		})
	}
	return out
}

// Gate mirrors one pending approval for the modal.
type Gate struct {
	To    string `json:"to"`
	Task  string `json:"task"`
	Brief string `json:"brief"`
	At    int64  `json:"at"`
}

func toGates(in []wire.Gate) []Gate {
	out := make([]Gate, 0, len(in))
	for _, g := range in {
		out = append(out, Gate{To: g.To, Task: g.Cmd.Task, Brief: g.Cmd.Brief, At: g.At / 1e6})
	}
	return out
}

// uiSocket is the attach socket for a session directory (ipc.UISocketPath is cwd-bound).
func uiSocket(dir string) string {
	return filepath.Join(ipc.SessionDir(), ipc.SessionID(dir)+".ui.sock")
}

// Attach connects read-only to dir's session and starts streaming events to the
// frontend; the ring replay arrives as ordinary pane:output events, then
// session:ready, then the live stream. Subscribe before calling.
func (a *App) Attach(dir string) (*Roster, error) {
	a.Detach() // one attach at a time; drop any previous one first
	conn, welcome, err := wire.Dial(uiSocket(dir), a.version)
	if err != nil {
		return nil, fmt.Errorf("attach: %w", err)
	}
	a.mu.Lock()
	a.conn = conn
	a.gen++
	gen := a.gen
	a.mu.Unlock()
	slog.Info("attached", "dir", dir, "roles", len(welcome.Roster))
	go a.stream(conn, gen)
	return &Roster{Roles: toRoles(welcome.Roster)}, nil
}

// stream replays the rings and pumps live frames into frontend events until the
// connection dies; gen guards against events from a superseded attach.
func (a *App) stream(conn *wire.Conn, gen int) {
	emitOutput := func(idx int, chunk []byte) {
		if a.current(gen) {
			runtime.EventsEmit(a.ctx, "pane:output", idx, base64.StdEncoding.EncodeToString(chunk))
		}
	}
	if err := conn.Replay(emitOutput); err != nil {
		a.lost(gen, fmt.Errorf("attach replay: %w", err))
		return
	}
	runtime.EventsEmit(a.ctx, "session:ready")
	err := conn.Pump(emitOutput, func(ev wire.Event) {
		if !a.current(gen) {
			return
		}
		switch ev.Kind {
		case "roster":
			runtime.EventsEmit(a.ctx, "session:roster", toRoles(ev.Roster))
		case "board":
			runtime.EventsEmit(a.ctx, "session:board", toTasks(ev.Board))
		case "gates":
			runtime.EventsEmit(a.ctx, "session:gates", toGates(ev.Gates))
		case "status":
			runtime.EventsEmit(a.ctx, "session:status", ev.On, ev.Up)
		case "bell":
			runtime.EventsEmit(a.ctx, "session:bell")
		case "reset":
			runtime.EventsEmit(a.ctx, "pane:reset", ev.Idx)
		case "focus":
			runtime.EventsEmit(a.ctx, "session:focus", ev.Idx)
		}
	})
	a.lost(gen, err)
}

// current reports whether gen is still the active attach.
func (a *App) current(gen int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gen == gen && a.conn != nil
}

// lost tells the frontend the active attach died; superseded attaches stay silent.
func (a *App) lost(gen int, err error) {
	a.mu.Lock()
	active := a.gen == gen && a.conn != nil
	if active {
		a.conn = nil
	}
	a.mu.Unlock()
	if active && a.ctx != nil {
		slog.Warn("attach lost", "err", err)
		runtime.EventsEmit(a.ctx, "session:lost", err.Error())
	}
}

// write ships one client event to the attached session.
func (a *App) write(ev wire.Event) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not attached")
	}
	return conn.WriteEvent(ev)
}

// Input forwards keystrokes (base64 bytes from xterm onData) to a pane's PTY.
func (a *App) Input(idx int, dataB64 string) error {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return err
	}
	return a.write(wire.Event{Kind: "input", Idx: idx, Data: data})
}

// Resize sets a pane's PTY size, like the TUI does for visible tiles.
func (a *App) Resize(idx, cols, rows int) error {
	return a.write(wire.Event{Kind: "resize", Idx: idx, Cols: cols, Rows: rows})
}

// Gate resolves the oldest pending approval; the server's gates event resyncs.
func (a *App) Gate(approve bool) error {
	return a.write(wire.Event{Kind: "gate", Approve: approve})
}

// RestartRole respawns a role's process.
func (a *App) RestartRole(idx int) error {
	return a.write(wire.Event{Kind: "restart", Idx: idx})
}

// PauseRole toggles SIGSTOP/SIGCONT on a role's process group.
func (a *App) PauseRole(idx int) error {
	return a.write(wire.Event{Kind: "pause", Idx: idx})
}

// StopSession shuts the whole session down, agents included.
func (a *App) StopSession() error {
	return a.write(wire.Event{Kind: "quit"})
}

// maxBriefBytes caps brief viewing so a runaway file cannot wedge the webview.
const maxBriefBytes = 1 << 20

// FileContent reads a brief or report file for the viewer modal.
func (a *App) FileContent(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(b) > maxBriefBytes {
		b = b[:maxBriefBytes]
	}
	return string(b), nil
}

// Detach drops the attach connection; the session keeps running.
func (a *App) Detach() {
	a.mu.Lock()
	conn := a.conn
	a.conn = nil
	a.mu.Unlock()
	if conn != nil {
		_ = conn.WriteEvent(wire.Event{Kind: "detach"})
		_ = conn.Close()
	}
}
