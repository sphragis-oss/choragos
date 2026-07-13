// SPDX-License-Identifier: Apache-2.0

package pane_test

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/pane"
)

func TestPaneCapturesOutput(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", "printf 'hello-choragos'"), 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	done := make(chan struct{})
	go func() {
		_ = p.Stream(nil)
		close(done)
	}()

	// poll Render concurrently with Stream to exercise the lock path under -race
	deadline := time.After(3 * time.Second)
	for {
		if strings.Contains(p.Render(), "hello-choragos") {
			return
		}
		select {
		case <-done:
			if !strings.Contains(p.Render(), "hello-choragos") {
				t.Fatalf("screen missing output:\n%q", p.Render())
			}
			return
		case <-deadline:
			t.Fatalf("timed out; screen:\n%q", p.Render())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestPaneRendersColor(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", `printf '\033[31mRED\033[0m'`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	done := make(chan struct{})
	go func() {
		_ = p.Stream(nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}

	out := p.Render()
	if !strings.Contains(out, "RED") {
		t.Fatalf("text lost:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[0;31m") {
		t.Fatalf("red foreground not preserved:\n%q", out)
	}
}

func TestPaneRendersTruecolor(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", `printf '\033[38;2;255;100;0mORANGE\033[0m'`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	done := make(chan struct{})
	go func() {
		_ = p.Stream(nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}

	out := p.Render()
	if !strings.Contains(out, "\x1b[0;38;2;255;100;0m") {
		t.Fatalf("truecolor foreground not preserved:\n%q", out)
	}
}

func TestShutdownDoesNotHang(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", `trap "" TERM; sleep 60`), 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Stream(nil) }()

	done := make(chan struct{})
	go func() { p.Shutdown(time.Now().Add(200 * time.Millisecond)); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown hung on a child that ignores SIGTERM")
	}
}

func TestScrollback(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", `for i in $(seq 1 30); do printf 'line%02d\r\n' "$i"; done`), 20, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	done := make(chan struct{})
	go func() { _ = p.Stream(nil); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}

	view, maxOff := p.Scrollback(20, 5, 0)
	if maxOff <= 0 {
		t.Fatalf("expected positive maxOffset, got %d", maxOff)
	}
	if !strings.Contains(view, "line30") {
		t.Fatalf("offset 0 should show newest line; got:\n%q", view)
	}
	top, _ := p.Scrollback(20, 5, maxOff)
	if !strings.Contains(top, "line01") {
		t.Fatalf("max offset should reach oldest captured line; got:\n%q", top)
	}
}

func TestPaneInput(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", `read x; printf 'got:%s' "$x"`), 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	done := make(chan struct{})
	go func() {
		_ = p.Stream(nil)
		close(done)
	}()

	time.Sleep(150 * time.Millisecond) // let `read` reach stdin
	if err := p.Input([]byte("world\r")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("child did not exit after input")
	}
	if !strings.Contains(p.Render(), "got:world") {
		t.Fatalf("input not delivered:\n%q", p.Render())
	}
}

func TestSeqAdvancesOnOutputAndResize(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", "printf 'seq-test'"), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Seq() != 0 {
		t.Fatalf("fresh pane Seq = %d, want 0", p.Seq())
	}
	done := make(chan struct{})
	go func() { _ = p.Stream(nil); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}
	after := p.Seq()
	if after == 0 {
		t.Fatal("output must advance Seq")
	}
	if err := p.Resize(30, 4); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if p.Seq() <= after {
		t.Fatal("resize must advance Seq")
	}
}

func TestCloseDoesNotLeakGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		p, err := pane.Start(exec.Command("cat"), 20, 5)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := p.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	// poll: reap goroutines need a moment to observe the killed children
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutines leaked: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestCloseWritesPlainTranscript(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", `printf '\033[1;31malpha\033[0m line\nbeta line\n'`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	p.SetLog(&log)

	done := make(chan struct{})
	go func() {
		_ = p.Stream(nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}
	_ = p.Close()

	got := log.String()
	if !strings.Contains(got, "alpha line") || !strings.Contains(got, "beta line") {
		t.Fatalf("transcript missing content:\n%q", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("transcript contains escape sequences:\n%q", got)
	}
}

func TestExitCodeCaptured(t *testing.T) {
	p, err := pane.Start(exec.Command("sh", "-c", "exit 3"), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	if p.ExitCode() != -1 {
		t.Fatalf("exit code before reap = %d, want -1", p.ExitCode())
	}
	done := make(chan struct{})
	go func() {
		_ = p.Stream(nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}
	_ = p.Close()
	if p.ExitCode() != 3 {
		t.Fatalf("exit code = %d, want 3", p.ExitCode())
	}
}

func TestRemotePaneFeedRenderInput(t *testing.T) {
	var sent [][]byte
	var resized [][2]int
	p := pane.Remote(20, 4, func(b []byte) error { sent = append(sent, b); return nil },
		func(c, r int) { resized = append(resized, [2]int{c, r}) })
	seq0 := p.Seq()
	p.Feed([]byte("hello remote"))
	if p.Seq() == seq0 {
		t.Fatal("Feed did not bump Seq")
	}
	if got := p.Render(); !strings.Contains(got, "hello remote") {
		t.Fatalf("render = %q", got)
	}
	if err := p.Input([]byte("keys")); err != nil || len(sent) != 1 || string(sent[0]) != "keys" {
		t.Fatalf("input not forwarded: err=%v sent=%v", err, sent)
	}
	if err := p.Resize(30, 5); err != nil || len(resized) != 1 || resized[0] != [2]int{30, 5} {
		t.Fatalf("resize not forwarded: err=%v resized=%v", err, resized)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Input([]byte("x")); err == nil {
		t.Fatal("input after close must error")
	}
}

func TestTeeAndRingBytesSequence(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf one; sleep 0.2; printf two; sleep 0.5")
	p, err := pane.Start(cmd, 40, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	type chunk struct {
		b   string
		seq uint64
	}
	var mu sync.Mutex
	var chunks []chunk
	p.SetTee(func(b []byte, seq uint64) {
		mu.Lock()
		chunks = append(chunks, chunk{string(b), seq})
		mu.Unlock()
	})
	done := make(chan struct{})
	go func() { _ = p.Stream(nil); close(done) }()
	<-done

	ring, snapSeq := p.RingBytes()
	if !strings.Contains(string(ring), "one") || !strings.Contains(string(ring), "two") {
		t.Fatalf("ring = %q", ring)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("tee never fired")
	}
	// every teed chunk is covered by the final snapshot sequence: replay + filter loses nothing
	for _, c := range chunks {
		if c.seq > snapSeq {
			t.Fatalf("chunk seq %d beyond snapshot seq %d", c.seq, snapSeq)
		}
	}
}
