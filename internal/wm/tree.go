// SPDX-License-Identifier: Apache-2.0

// Package wm is a pure binary layout tree tiling the fixed role panes.
package wm

const (
	minRatio = 0.1
	maxRatio = 0.9
	// below these outer dims a tile is unusable; Layout degrades to the focused tile only
	minTileW = 12
	minTileH = 4
)

// Dir is a geometric focus direction.
type Dir int

const (
	Left Dir = iota
	Down
	Up
	Right
)

// node is a leaf (tile bound to a role index) or a split with a first-child share.
type node struct {
	leaf   bool
	role   int
	vert   bool // true: children side by side (vertical divider)
	ratio  float64
	a, b   *node
	parent *node
}

// Tile is one visible leaf's computed rectangle.
type Tile struct {
	Role       int
	X, Y, W, H int
	Focused    bool
}

// placed pairs a leaf with its computed rectangle.
type placed struct {
	n          *node
	x, y, w, h int
}

// Tree holds the layout plus focus, zoom, and resize-mode state.
type Tree struct {
	root     *node
	focused  *node
	zoomed   bool
	resizing bool
}

// New returns a tree with a single tile bound to role.
func New(role int) *Tree {
	n := &node{leaf: true, role: role}
	return &Tree{root: n, focused: n}
}

// leaves returns the tiles in tree order.
func (t *Tree) leaves() []*node {
	var out []*node
	var walk func(n *node)
	walk = func(n *node) {
		if n.leaf {
			out = append(out, n)
			return
		}
		walk(n.a)
		walk(n.b)
	}
	walk(t.root)
	return out
}

// VisibleRoles returns the role index of every tile in tree order.
func (t *Tree) VisibleRoles() []int {
	var out []int
	for _, n := range t.leaves() {
		out = append(out, n.role)
	}
	return out
}

// FocusedRole returns the focused tile's role index.
func (t *Tree) FocusedRole() int { return t.focused.role }

// Zoomed reports whether the focused tile is fullscreened.
func (t *Tree) Zoomed() bool { return t.zoomed }

// Resizing reports whether resize mode is active.
func (t *Tree) Resizing() bool { return t.resizing }

// SetResizing toggles resize mode.
func (t *Tree) SetResizing(on bool) { t.resizing = on }

// ToggleZoom fullscreens the focused tile; toggling off restores the tree untouched.
func (t *Tree) ToggleZoom() { t.zoomed = !t.zoomed }

// FocusRole focuses the tile showing role, reporting whether it is visible.
func (t *Tree) FocusRole(role int) bool {
	for _, n := range t.leaves() {
		if n.role == role {
			t.focused = n
			return true
		}
	}
	return false
}

// Focus shows role: focuses its tile when visible, else retargets the focused tile.
func (t *Tree) Focus(role int) {
	if t.FocusRole(role) {
		return
	}
	t.focused.role = role
}

// Split divides the focused tile, binding the new tile to role and focusing it.
func (t *Tree) Split(vert bool, role int) {
	t.zoomed = false
	f := t.focused
	a := &node{leaf: true, role: f.role, parent: f}
	b := &node{leaf: true, role: role, parent: f}
	f.leaf, f.role, f.vert, f.ratio, f.a, f.b = false, 0, vert, 0.5, a, b
	t.focused = b
}

// Close removes the focused tile, hoisting its sibling; no-op on the last tile.
func (t *Tree) Close() bool {
	t.zoomed = false
	p := t.focused.parent
	if p == nil {
		return false
	}
	keep := p.a
	if keep == t.focused {
		keep = p.b
	}
	gp := p.parent
	*p = *keep
	p.parent = gp
	if !p.leaf {
		p.a.parent, p.b.parent = p, p
	}
	for n := p; ; n = n.a {
		if n.leaf {
			t.focused = n
			break
		}
	}
	return true
}

// CycleNext focuses the next tile in tree order.
func (t *Tree) CycleNext() { t.cycle(1) }

// CyclePrev focuses the previous tile in tree order.
func (t *Tree) CyclePrev() { t.cycle(-1) }

func (t *Tree) cycle(step int) {
	ls := t.leaves()
	for i, n := range ls {
		if n == t.focused {
			t.focused = ls[(i+step+len(ls))%len(ls)]
			return
		}
	}
}

// AdjustRatio moves the divider of the nearest ancestor split with orientation vert.
func (t *Tree) AdjustRatio(vert bool, delta float64) bool {
	for n := t.focused.parent; n != nil; n = n.parent {
		if n.vert == vert {
			n.ratio = clampRatio(n.ratio + delta)
			return true
		}
	}
	return false
}

func clampRatio(r float64) float64 {
	if r < minRatio {
		return minRatio
	}
	if r > maxRatio {
		return maxRatio
	}
	return r
}

// Layout computes visible tile rects for a w x h area, degrading to the
// focused tile alone when zoomed or when any tile would be unusably small.
func (t *Tree) Layout(w, h int) []Tile {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	focusedOnly := []Tile{{Role: t.focused.role, W: w, H: h, Focused: true}}
	if t.zoomed {
		return focusedOnly
	}
	tiles := make([]Tile, 0, 4)
	for _, p := range t.rects(w, h) {
		if p.w < minTileW || p.h < minTileH {
			return focusedOnly
		}
		tiles = append(tiles, Tile{Role: p.n.role, X: p.x, Y: p.y, W: p.w, H: p.h, Focused: p.n == t.focused})
	}
	return tiles
}

// rects places every leaf without the fallback checks.
func (t *Tree) rects(w, h int) []placed {
	var out []placed
	var walk func(n *node, x, y, w, h int)
	walk = func(n *node, x, y, w, h int) {
		if n.leaf {
			out = append(out, placed{n: n, x: x, y: y, w: w, h: h})
			return
		}
		if n.vert {
			wa, wb := splitDim(w, n.ratio)
			walk(n.a, x, y, wa, h)
			walk(n.b, x+wa, y, wb, h)
			return
		}
		ha, hb := splitDim(h, n.ratio)
		walk(n.a, x, y, w, ha)
		walk(n.b, x, y+ha, w, hb)
	}
	walk(t.root, 0, 0, w, h)
	return out
}

// splitDim divides total by ratio, keeping both halves at least 1 when possible.
func splitDim(total int, ratio float64) (int, int) {
	if total < 2 {
		return total, 0
	}
	a := int(float64(total)*ratio + 0.5)
	if a < 1 {
		a = 1
	}
	if a > total-1 {
		a = total - 1
	}
	return a, total - a
}

// FocusDir moves focus to the geometrically adjacent tile in a w x h area.
func (t *Tree) FocusDir(d Dir, w, h int) bool {
	if t.zoomed {
		return false
	}
	ps := t.rects(w, h)
	var f placed
	for _, p := range ps {
		if p.n == t.focused {
			f = p
		}
	}
	var best *node
	bestDist, bestOverlap := 0, 0
	for _, c := range ps {
		if c.n == t.focused {
			continue
		}
		var dist, overlap int
		switch d {
		case Left:
			dist, overlap = f.x-(c.x+c.w), overlap1D(f.y, f.h, c.y, c.h)
		case Right:
			dist, overlap = c.x-(f.x+f.w), overlap1D(f.y, f.h, c.y, c.h)
		case Up:
			dist, overlap = f.y-(c.y+c.h), overlap1D(f.x, f.w, c.x, c.w)
		case Down:
			dist, overlap = c.y-(f.y+f.h), overlap1D(f.x, f.w, c.x, c.w)
		}
		if dist < 0 || overlap <= 0 {
			continue
		}
		if best == nil || dist < bestDist || (dist == bestDist && overlap > bestOverlap) {
			best, bestDist, bestOverlap = c.n, dist, overlap
		}
	}
	if best == nil {
		return false
	}
	t.focused = best
	return true
}

// overlap1D returns the overlap of segments [a,a+al) and [b,b+bl).
func overlap1D(a, al, b, bl int) int {
	lo, hi := max(a, b), min(a+al, b+bl)
	return hi - lo
}
