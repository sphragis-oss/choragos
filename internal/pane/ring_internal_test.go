// SPDX-License-Identifier: Apache-2.0

package pane

import "testing"

func TestRingPartialFill(t *testing.T) {
	r := newRing(8)
	r.Write([]byte("abc"))
	if got := string(r.Snapshot()); got != "abc" {
		t.Fatalf("partial fill snapshot = %q, want %q", got, "abc")
	}
}

func TestRingExactBoundaryFill(t *testing.T) {
	r := newRing(8)
	r.Write([]byte("1234"))
	r.Write([]byte("5678")) // lands exactly on the buffer boundary
	if got := string(r.Snapshot()); got != "12345678" {
		t.Fatalf("exact-boundary snapshot = %q, want %q", got, "12345678")
	}
}

func TestRingWrapKeepsTail(t *testing.T) {
	r := newRing(8)
	r.Write([]byte("abcdef"))
	r.Write([]byte("ghij")) // wraps: only the newest 8 bytes survive
	if got := string(r.Snapshot()); got != "cdefghij" {
		t.Fatalf("wrapped snapshot = %q, want %q", got, "cdefghij")
	}
}

func TestRingOversizeWriteKeepsTail(t *testing.T) {
	r := newRing(4)
	r.Write([]byte("abcdefgh"))
	if got := string(r.Snapshot()); got != "efgh" {
		t.Fatalf("oversize snapshot = %q, want %q", got, "efgh")
	}
}

func TestRingWriteAtCapacityExactly(t *testing.T) {
	r := newRing(4)
	r.Write([]byte("abcd")) // len(p) == max
	if got := string(r.Snapshot()); got != "abcd" {
		t.Fatalf("at-capacity snapshot = %q, want %q", got, "abcd")
	}
	r.Write([]byte("ef"))
	if got := string(r.Snapshot()); got != "cdef" {
		t.Fatalf("post-capacity snapshot = %q, want %q", got, "cdef")
	}
}
