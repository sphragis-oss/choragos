// SPDX-License-Identifier: Apache-2.0

package pane

import "testing"

func TestHistTerminalCachesPerSeqAndCols(t *testing.T) {
	p := &Pane{hist: newRing(ringCap)}
	p.hist.Write([]byte("hello\r\nworld\r\n"))
	p.seq.Add(1)

	h1, top, _ := p.histTerminal(20)
	if top < 0 {
		t.Fatal("expected content in the history terminal")
	}
	if h2, _, _ := p.histTerminal(20); h2 != h1 {
		t.Fatal("unchanged ring must reuse the parsed terminal")
	}
	h3, _, _ := p.histTerminal(30)
	if h3 == h1 {
		t.Fatal("a width change must rebuild the terminal")
	}
	p.hist.Write([]byte("more\r\n"))
	p.seq.Add(1)
	if h4, _, _ := p.histTerminal(30); h4 == h3 {
		t.Fatal("new output must rebuild the terminal")
	}
}

func TestScrollbackWindowsReuseOneParse(t *testing.T) {
	p := &Pane{hist: newRing(ringCap)}
	for i := 0; i < 30; i++ {
		p.hist.Write([]byte("line\r\n"))
	}
	p.seq.Add(1)
	_, maxOff := p.Scrollback(20, 5, 0)
	if maxOff <= 0 {
		t.Fatalf("expected positive maxOffset, got %d", maxOff)
	}
	before, _, _ := p.histTerminal(20)
	_, _ = p.Scrollback(20, 5, maxOff) // different offset, same parse
	after, _, _ := p.histTerminal(20)
	if before != after {
		t.Fatal("re-windowing must not re-parse the history")
	}
}
