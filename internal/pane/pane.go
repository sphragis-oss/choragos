// SPDX-License-Identifier: Apache-2.0

// Package pane runs a child process in a PTY that choragos owns and parses.
package pane

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// ErrPaneClosed reports input sent to a pane after Close.
var ErrPaneClosed = errors.New("pane closed")

// ErrInputDropped reports input dropped because the child stopped draining its PTY.
var ErrInputDropped = errors.New("input dropped: inbox full")

// shutdownPoll is how often Shutdown checks whether a terminated child has exited.
const shutdownPoll = 50 * time.Millisecond

// reapTimeout bounds the wait for a killed child so a wedged process can never hang quit.
const reapTimeout = 2 * time.Second

// inboxCap buffers queued keystrokes so the UI loop never blocks on PTY backpressure.
const inboxCap = 256

// Mirror of vt10x's unexported attribute bits for the pinned version.
const (
	attrReverse   = 1 << 0
	attrUnderline = 1 << 1
	attrBold      = 1 << 2
	attrItalic    = 1 << 4
	attrBlink     = 1 << 5
)

// defaultColor is vt10x's threshold: values >= 1<<24 are default/special colors.
const defaultColor = 1 << 24

// SGR parameter bases: standard / bright color offsets and extended-color introducers.
const (
	sgrFgBase   = 30
	sgrBgBase   = 40
	sgrFgBright = 90
	sgrBgBright = 100
	sgrFgExt    = "38"
	sgrBgExt    = "48"
)

// Scrollback keeps a capped raw-byte history replayed into a tall emulator on demand.
const (
	ringCap        = 256 * 1024
	scrollbackRows = 500
)

// ring is a capped raw-byte history of a pane's PTY output.
type ring struct {
	mu   sync.Mutex
	buf  []byte
	head int
	full bool
}

func newRing(max int) *ring { return &ring{buf: make([]byte, max)} }

func (r *ring) Write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	max := len(r.buf)
	if len(p) >= max {
		// If input exceeds capacity, just keep the tail
		copy(r.buf, p[len(p)-max:])
		r.head = 0
		r.full = true
		return
	}
	n := copy(r.buf[r.head:], p)
	if n < len(p) {
		// Wrap around
		copy(r.buf, p[n:])
		r.full = true
	}
	r.head = (r.head + len(p)) % max
	if r.head == 0 && len(p) > 0 {
		r.full = true // landed exactly on the boundary: the buffer is full, not empty
	}
}

func (r *ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]byte, r.head)
		copy(out, r.buf[:r.head])
		return out
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf[r.head:])
	copy(out[len(r.buf)-r.head:], r.buf[:r.head])
	return out
}

// histCache holds the replayed-history terminal, rebuilt only when the ring or width changes.
type histCache struct {
	mu   sync.Mutex
	term vt10x.Terminal
	seq  uint64
	cols int
	top  int
	bot  int // inclusive last content row; -1 when the history is blank
}

// Pane is one agent process bound to a PTY and a virtual-terminal emulator.
type Pane struct {
	cmd       *exec.Cmd
	ptmx      *os.File
	term      vt10x.Terminal
	hist      *ring
	hcache    histCache
	logw      io.Writer
	inbox     chan []byte
	done      chan struct{}
	closeOnce sync.Once
	seq       atomic.Uint64 // bumped on every content-affecting change; render-cache key
}

// Seq returns a counter that advances whenever the screen may have changed (output or resize).
func (p *Pane) Seq() uint64 { return p.seq.Load() }

// Start launches cmd in a PTY sized cols x rows.
func Start(cmd *exec.Cmd, cols, rows int) (*Pane, error) {
	ptmx, err := pty.StartWithSize(cmd, winsize(cols, rows))
	if err != nil {
		return nil, err
	}
	term := vt10x.New(vt10x.WithWriter(ptmx), vt10x.WithSize(cols, rows))
	p := &Pane{cmd: cmd, ptmx: ptmx, term: term, hist: newRing(ringCap), inbox: make(chan []byte, inboxCap), done: make(chan struct{})}
	go p.writeLoop()
	return p, nil
}

// writeLoop drains queued input to the PTY off the UI thread; a blocking write can never freeze the deck.
func (p *Pane) writeLoop() {
	for {
		select {
		case b := <-p.inbox:
			if _, err := p.ptmx.Write(b); err != nil {
				return
			}
		case <-p.done:
			return
		}
	}
}

// SetLog sets the sink that receives the plain-text transcript when the pane closes.
func (p *Pane) SetLog(w io.Writer) { p.logw = w }

// Stream copies PTY output into the emulator until read error, calling onFrame per chunk; blocks.
func (p *Pane) Stream(onFrame func()) error {
	buf := make([]byte, 4096)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, _ = p.term.Write(chunk)
			p.hist.Write(chunk)
			p.seq.Add(1)
			if onFrame != nil {
				onFrame()
			}
		}
		if err != nil {
			return err
		}
	}
}

// Input queues keystrokes for the child; it never blocks the caller so PTY backpressure cannot freeze the UI loop.
// A full inbox means the child stopped draining its PTY; the input is dropped rather than queued forever.
func (p *Pane) Input(b []byte) error {
	select {
	case <-p.done:
		return ErrPaneClosed
	default:
	}
	cp := append([]byte(nil), b...) // caller may reuse b
	select {
	case p.inbox <- cp:
		return nil
	case <-p.done:
		return ErrPaneClosed
	default:
		return ErrInputDropped
	}
}

// Resize updates both the emulator and the PTY window size.
func (p *Pane) Resize(cols, rows int) error {
	cols, rows = clampDim(cols), clampDim(rows)
	p.term.Resize(cols, rows)
	p.seq.Add(1) // reflow changes the rendered screen without new output
	return pty.Setsize(p.ptmx, winsize(cols, rows))
}

// Render returns the live screen as ANSI-colored text, preserving colors and attributes.
func (p *Pane) Render() string {
	p.term.Lock()
	defer p.term.Unlock()
	cols, rows := p.term.Size()
	return renderRows(p.term, cols, 0, rows)
}

// histTerminal returns the replayed-history terminal and its content bounds, re-parsing the
// ring only when it changed or the width differs; moving the scroll window is then free.
func (p *Pane) histTerminal(cols int) (vt10x.Terminal, int, int) {
	c := &p.hcache
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := p.seq.Load(); c.term == nil || c.seq != s || c.cols != cols {
		h := vt10x.New(vt10x.WithWriter(io.Discard), vt10x.WithSize(cols, scrollbackRows))
		_, _ = h.Write(p.hist.Snapshot())
		h.Lock()
		top, bot := contentBounds(h, cols)
		h.Unlock()
		c.term, c.seq, c.cols, c.top, c.bot = h, s, cols, top, bot
	}
	return c.term, c.top, c.bot
}

// contentBounds returns the first and last non-blank rows, or -1s; caller holds the lock.
func contentBounds(t vt10x.Terminal, cols int) (top, bot int) {
	top, bot = -1, -1
	_, rows := t.Size()
	for y := 0; y < rows; y++ {
		if !rowBlank(t, cols, y) {
			if top < 0 {
				top = y
			}
			bot = y
		}
	}
	return top, bot
}

// Scrollback windows the replayed history: a height-tall view offset rows above the live
// bottom, plus the maximum offset the history allows.
func (p *Pane) Scrollback(cols, height, offset int) (view string, maxOffset int) {
	if cols < 1 || height < 1 {
		return "", 0
	}
	h, top, bot := p.histTerminal(cols)
	if top < 0 {
		return "", 0
	}
	h.Lock()
	defer h.Unlock()
	botRow := bot + 1 // exclusive end of real content; anchors the live bottom
	maxOffset = (botRow - top) - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	end := botRow - offset
	start := end - height
	if start < top {
		start = top
	}
	return renderRows(h, cols, start, end), maxOffset
}

// HistoryLines replays the captured history and returns its plain-text content rows.
// Row indices align with Scrollback: for L rows and a height-tall view, offset o shows rows [L-o-height, L-o).
func (p *Pane) HistoryLines(cols int) []string {
	if cols < 1 {
		return nil
	}
	h, top, bot := p.histTerminal(cols)
	if top < 0 {
		return nil
	}
	h.Lock()
	defer h.Unlock()
	var out []string
	for y := top; y <= bot; y++ {
		var sb strings.Builder
		for x := 0; x < cols; x++ {
			ch := h.Cell(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			sb.WriteRune(ch)
		}
		out = append(out, strings.TrimRight(sb.String(), " "))
	}
	return out
}

// renderRows emits rows [y0,y1) of a terminal as ANSI-colored text; caller holds the lock.
func renderRows(t vt10x.Terminal, cols, y0, y1 int) string {
	var b strings.Builder
	prev := ""
	for y := y0; y < y1; y++ {
		for x := 0; x < cols; x++ {
			g := t.Cell(x, y)
			if s := sgr(g); s != prev {
				b.WriteString(s)
				prev = s
			}
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		b.WriteString("\x1b[0m")
		prev = ""
		if y < y1-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// rowBlank reports whether screen row y is empty; caller holds the lock.
func rowBlank(t vt10x.Terminal, cols, y int) bool {
	for x := 0; x < cols; x++ {
		if ch := t.Cell(x, y).Char; ch != 0 && ch != ' ' {
			return false
		}
	}
	return true
}

// TailLines returns up to n most recent non-blank screen rows as plain text, for a collapsed activity preview.
func (p *Pane) TailLines(n int) []string {
	p.term.Lock()
	defer p.term.Unlock()

	cols, rows := p.term.Size()
	var lines []string
	for y := 0; y < rows; y++ {
		var sb strings.Builder
		for x := 0; x < cols; x++ {
			ch := p.term.Cell(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			sb.WriteRune(ch)
		}
		if s := strings.TrimRight(sb.String(), " "); s != "" {
			lines = append(lines, s)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// sgr builds the ANSI escape that reproduces a glyph's attributes and colors.
func sgr(g vt10x.Glyph) string {
	params := []string{"0"}
	if g.Mode&attrBold != 0 {
		params = append(params, "1")
	}
	if g.Mode&attrItalic != 0 {
		params = append(params, "3")
	}
	if g.Mode&attrUnderline != 0 {
		params = append(params, "4")
	}
	if g.Mode&attrBlink != 0 {
		params = append(params, "5")
	}
	if g.Mode&attrReverse != 0 {
		params = append(params, "7")
	}
	params = append(params, colorParams(g.FG, sgrFgBase, sgrFgBright, sgrFgExt)...)
	params = append(params, colorParams(g.BG, sgrBgBase, sgrBgBright, sgrBgExt)...)
	return "\x1b[" + strings.Join(params, ";") + "m"
}

// colorParams maps a vt10x color to SGR params (ANSI, 256, or packed 24-bit truecolor); default emits nothing.
func colorParams(c vt10x.Color, base, bright int, ext string) []string {
	switch {
	case c >= defaultColor:
		return nil
	case c < 8:
		return []string{strconv.Itoa(base + int(c))}
	case c < 16:
		return []string{strconv.Itoa(bright + int(c-8))}
	case c < 256:
		return []string{ext, "5", strconv.Itoa(int(c))}
	default:
		r, g, b := (c>>16)&0xff, (c>>8)&0xff, c&0xff
		return []string{ext, "2", strconv.Itoa(int(r)), strconv.Itoa(int(g)), strconv.Itoa(int(b))}
	}
}

// Close force-stops the child (SIGKILL), releases the PTY, writes the transcript, and closes the log sink; idempotent.
func (p *Pane) Close() error {
	p.closeOnce.Do(func() {
		close(p.done)      // release writeLoop and refuse further input
		_ = p.ptmx.Close() // close the master first: unblocks the child's tty I/O and our reader so Wait can return
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		p.reap()
		p.writeTranscript()
		if c, ok := p.logw.(io.Closer); ok {
			_ = c.Close()
		}
	})
	return nil
}

// writeTranscript renders the captured history as plain text into the log sink: what the user saw, not the wire bytes.
func (p *Pane) writeTranscript() {
	if p.logw == nil {
		return
	}
	p.term.Lock()
	cols, _ := p.term.Size()
	p.term.Unlock()
	for _, l := range p.HistoryLines(cols) {
		_, _ = io.WriteString(p.logw, l+"\n")
	}
}

// reap waits for the killed child, bounded so a wedged process can never hang shutdown.
func (p *Pane) reap() {
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(reapTimeout):
	}
}

// Terminate asks the child to exit cleanly (SIGTERM) so it can run its own shutdown hooks.
func (p *Pane) Terminate() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}
}

// Shutdown waits until deadline for a terminated child to exit (running its hooks), then force-closes.
func (p *Pane) Shutdown(deadline time.Time) {
	for time.Now().Before(deadline) && !p.exited() {
		time.Sleep(shutdownPoll)
	}
	_ = p.Close()
}

// exited reports whether the child process is gone, probed with signal 0.
func (p *Pane) exited() bool {
	if p.cmd.Process == nil {
		return true
	}
	return p.cmd.Process.Signal(syscall.Signal(0)) != nil
}

// winsize builds a PTY window size, clamping to at least 1 so a non-positive dim never wraps uint16.
func winsize(cols, rows int) *pty.Winsize {
	return &pty.Winsize{Cols: uint16(clampDim(cols)), Rows: uint16(clampDim(rows))}
}

func clampDim(v int) int {
	if v < 1 {
		return 1
	}
	return v
}
