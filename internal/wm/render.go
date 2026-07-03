// SPDX-License-Identifier: Apache-2.0

package wm

import "github.com/charmbracelet/lipgloss"

// Render composes the visible tiles for a w x h area, calling content per tile.
// It mirrors Layout exactly: same split math, same focused-only fallback.
func (t *Tree) Render(w, h int, content func(role, w, h int) string) string {
	tiles := t.Layout(w, h)
	if len(tiles) == 1 {
		return content(tiles[0].Role, tiles[0].W, tiles[0].H)
	}
	return renderNode(t.root, w, h, content)
}

func renderNode(n *node, w, h int, content func(role, w, h int) string) string {
	if n.leaf {
		return content(n.role, w, h)
	}
	if n.vert {
		wa, wb := splitDim(w, n.ratio)
		return lipgloss.JoinHorizontal(lipgloss.Top,
			renderNode(n.a, wa, h, content), renderNode(n.b, wb, h, content))
	}
	ha, hb := splitDim(h, n.ratio)
	return lipgloss.JoinVertical(lipgloss.Left,
		renderNode(n.a, w, ha, content), renderNode(n.b, w, hb, content))
}
