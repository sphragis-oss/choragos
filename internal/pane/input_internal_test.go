// SPDX-License-Identifier: Apache-2.0

package pane

import (
	"errors"
	"testing"
	"time"
)

// stubPane builds a Pane with just the input machinery, no child process.
func stubPane(cap int) *Pane {
	return &Pane{inbox: make(chan []byte, cap), done: make(chan struct{})}
}

func TestInputDropsWhenInboxFull(t *testing.T) {
	p := stubPane(1)
	if err := p.Input([]byte("a")); err != nil {
		t.Fatalf("first input: %v", err)
	}
	start := time.Now()
	err := p.Input([]byte("b"))
	if !errors.Is(err, ErrInputDropped) {
		t.Fatalf("want ErrInputDropped, got %v", err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("Input blocked for %v on a full inbox", d)
	}
}

func TestInputAfterCloseReturnsErrPaneClosed(t *testing.T) {
	p := stubPane(1)
	close(p.done)
	if err := p.Input([]byte("a")); !errors.Is(err, ErrPaneClosed) {
		t.Fatalf("want ErrPaneClosed, got %v", err)
	}
}

func TestWriteLoopExitsOnDone(t *testing.T) {
	p := stubPane(1)
	exited := make(chan struct{})
	go func() { p.writeLoop(); close(exited) }()
	close(p.done)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop did not exit after done closed")
	}
}
