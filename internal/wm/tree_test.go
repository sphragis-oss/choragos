// SPDX-License-Identifier: Apache-2.0

package wm

import (
	"strings"
	"testing"
)

func roles(t *Tree) []int { return t.VisibleRoles() }

func eq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSplitAndVisibleRoles(t *testing.T) {
	tr := New(0)
	if !eq(roles(tr), []int{0}) || tr.FocusedRole() != 0 {
		t.Fatalf("new tree: roles=%v focused=%d", roles(tr), tr.FocusedRole())
	}
	tr.Split(true, 1)
	if !eq(roles(tr), []int{0, 1}) {
		t.Fatalf("after vsplit: %v", roles(tr))
	}
	if tr.FocusedRole() != 1 {
		t.Fatalf("focus should move to new tile, got %d", tr.FocusedRole())
	}
	tr.Split(false, 2)
	if !eq(roles(tr), []int{0, 1, 2}) || tr.FocusedRole() != 2 {
		t.Fatalf("after hsplit: roles=%v focused=%d", roles(tr), tr.FocusedRole())
	}
}

func TestCloseRebalancesAndRefusesLast(t *testing.T) {
	tr := New(0)
	if tr.Close() {
		t.Fatal("closing the last tile must be refused")
	}
	tr.Split(true, 1)
	tr.Split(false, 2)
	if !tr.Close() {
		t.Fatal("close failed")
	}
	if !eq(roles(tr), []int{0, 1}) {
		t.Fatalf("after close: %v", roles(tr))
	}
	if tr.FocusedRole() != 1 {
		t.Fatalf("focus after close = %d, want sibling 1", tr.FocusedRole())
	}
	if !tr.Close() || !eq(roles(tr), []int{0}) {
		t.Fatalf("close to root failed: %v", roles(tr))
	}
	if tr.Close() {
		t.Fatal("root leaf close must be refused")
	}
}

func TestCycle(t *testing.T) {
	tr := New(0)
	tr.Split(true, 1)
	tr.Split(true, 2) // order: 0, 1, 2; focused 2
	tr.CycleNext()
	if tr.FocusedRole() != 0 {
		t.Fatalf("cycle next wrapped to %d, want 0", tr.FocusedRole())
	}
	tr.CyclePrev()
	if tr.FocusedRole() != 2 {
		t.Fatalf("cycle prev = %d, want 2", tr.FocusedRole())
	}
}

func TestFocusRoleAndRetarget(t *testing.T) {
	tr := New(0)
	tr.Split(true, 1)
	if !tr.FocusRole(0) || tr.FocusedRole() != 0 {
		t.Fatal("FocusRole(0) should focus the visible tile")
	}
	if tr.FocusRole(4) {
		t.Fatal("FocusRole must report hidden roles")
	}
	tr.Focus(4) // hidden: retarget focused tile
	if tr.FocusedRole() != 4 || !eq(roles(tr), []int{4, 1}) {
		t.Fatalf("retarget: roles=%v focused=%d", roles(tr), tr.FocusedRole())
	}
	tr.Focus(1) // visible: just focus
	if tr.FocusedRole() != 1 || !eq(roles(tr), []int{4, 1}) {
		t.Fatalf("focus visible: roles=%v focused=%d", roles(tr), tr.FocusedRole())
	}
}

func TestZoomRestoresTree(t *testing.T) {
	tr := New(0)
	tr.Split(true, 1)
	tr.AdjustRatio(true, 0.2)
	before := tr.Layout(100, 40)
	tr.ToggleZoom()
	z := tr.Layout(100, 40)
	if len(z) != 1 || z[0].Role != 1 || z[0].W != 100 || z[0].H != 40 {
		t.Fatalf("zoomed layout = %+v", z)
	}
	tr.ToggleZoom()
	after := tr.Layout(100, 40)
	if len(after) != len(before) {
		t.Fatalf("tree changed across zoom: %+v vs %+v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("tile %d changed: %+v vs %+v", i, before[i], after[i])
		}
	}
}

func TestAdjustRatioClampsAndPicksOrientation(t *testing.T) {
	tr := New(0)
	tr.Split(true, 1) // vertical divider
	if tr.AdjustRatio(false, 0.05) {
		t.Fatal("no horizontal split to adjust")
	}
	for i := 0; i < 20; i++ {
		tr.AdjustRatio(true, 0.05)
	}
	tiles := tr.Layout(200, 40)
	if tiles[0].W != 180 {
		t.Fatalf("ratio should clamp at 0.9: first tile W=%d", tiles[0].W)
	}
	for i := 0; i < 40; i++ {
		tr.AdjustRatio(true, -0.05)
	}
	if tiles = tr.Layout(200, 40); tiles[0].W != 20 {
		t.Fatalf("ratio should clamp at 0.1: first tile W=%d", tiles[0].W)
	}
}

func TestLayoutDimsTileExactly(t *testing.T) {
	tr := New(0)
	tr.Split(true, 1)
	tr.Split(false, 2)
	tiles := tr.Layout(101, 41)
	area := 0
	for _, tile := range tiles {
		area += tile.W * tile.H
	}
	if area != 101*41 {
		t.Fatalf("tiles cover %d cells, want %d", area, 101*41)
	}
}

func TestLayoutTinyFallsBackToFocused(t *testing.T) {
	tr := New(0)
	tr.Split(true, 1)
	tr.Split(false, 2)
	tiles := tr.Layout(20, 6)
	if len(tiles) != 1 || !tiles[0].Focused || tiles[0].W != 20 || tiles[0].H != 6 {
		t.Fatalf("tiny layout = %+v", tiles)
	}
	if got := tr.Layout(0, -3); len(got) != 1 || got[0].W != 1 || got[0].H != 1 {
		t.Fatalf("non-positive dims must clamp, got %+v", got)
	}
}

func TestFocusDir(t *testing.T) {
	// 0 | 1 over 2 (right column split top/bottom)
	tr := New(0)
	tr.Split(true, 1)
	tr.Split(false, 2)
	if !tr.FocusDir(Up, 100, 40) || tr.FocusedRole() != 1 {
		t.Fatalf("up from 2 = %d, want 1", tr.FocusedRole())
	}
	if !tr.FocusDir(Left, 100, 40) || tr.FocusedRole() != 0 {
		t.Fatalf("left from 1 = %d, want 0", tr.FocusedRole())
	}
	if tr.FocusDir(Left, 100, 40) {
		t.Fatal("no tile left of 0")
	}
	if !tr.FocusDir(Right, 100, 40) || tr.FocusedRole() == 0 {
		t.Fatal("right from 0 should reach the right column")
	}
	tr.ToggleZoom()
	if tr.FocusDir(Left, 100, 40) {
		t.Fatal("focus dir must no-op while zoomed")
	}
}

func TestRenderComposesTiles(t *testing.T) {
	tr := New(0)
	tr.Split(true, 1)
	got := tr.Render(30, 4, func(role, w, h int) string {
		row := strings.Repeat(string(rune('a'+role)), w)
		return strings.Join([]string{row, row, row, row}, "\n")
	})
	row := strings.Repeat("a", 15) + strings.Repeat("b", 15)
	want := strings.Join([]string{row, row, row, row}, "\n")
	if got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
	// tiny area: only the focused tile renders
	got = tr.Render(8, 2, func(role, w, h int) string {
		return strings.Repeat(string(rune('a'+role)), w)
	})
	if got != "bbbbbbbb" {
		t.Fatalf("tiny render = %q", got)
	}
}
