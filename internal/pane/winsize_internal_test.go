// SPDX-License-Identifier: Apache-2.0

package pane

import "testing"

func TestWinsizeClampsNonPositive(t *testing.T) {
	// negative dims must not wrap around when cast to uint16 (uint16(-2) == 65534)
	if ws := winsize(-2, -3); ws.Cols != 1 || ws.Rows != 1 {
		t.Fatalf("winsize(-2,-3) = %dx%d, want 1x1", ws.Cols, ws.Rows)
	}
	if ws := winsize(0, 0); ws.Cols != 1 || ws.Rows != 1 {
		t.Fatalf("winsize(0,0) = %dx%d, want 1x1", ws.Cols, ws.Rows)
	}
	if ws := winsize(80, 24); ws.Cols != 80 || ws.Rows != 24 {
		t.Fatalf("winsize(80,24) = %dx%d, want 80x24", ws.Cols, ws.Rows)
	}
}

func TestWinsizeClampsOversized(t *testing.T) {
	// dims past uint16 must saturate at 65535, not wrap (uint16(1<<16) == 0)
	if ws := winsize(1<<16, 1<<20); ws.Cols != 0xFFFF || ws.Rows != 0xFFFF {
		t.Fatalf("winsize(1<<16,1<<20) = %dx%d, want 65535x65535", ws.Cols, ws.Rows)
	}
	if ws := winsize(0xFFFF, 0xFFFF); ws.Cols != 0xFFFF || ws.Rows != 0xFFFF {
		t.Fatalf("winsize at the cap = %dx%d, want 65535x65535", ws.Cols, ws.Rows)
	}
}
