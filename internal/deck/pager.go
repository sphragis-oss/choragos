// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// pagerMaxBytes caps how much of a brief or report the overlay loads.
const pagerMaxBytes = 512 * 1024

// openPager loads a markdown file into the pager overlay; the file is never modified.
func (m *Model) openPager(title, path string) {
	_, mainW, _ := m.dims()
	m.pagerTitle = title
	m.pagerLines = renderMarkdown(path, mainW-6)
	m.pagerOff = 0
	m.pagerOn = true
}

// renderMarkdown renders the file with glamour, falling back to the raw text.
func renderMarkdown(path string, width int) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("cannot read %s: %v", path, err)}
	}
	if len(b) > pagerMaxBytes {
		b = b[:pagerMaxBytes]
	}
	if width < 20 {
		width = 20
	}
	if r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(width)); err == nil {
		if out, err := r.Render(string(b)); err == nil {
			return strings.Split(strings.TrimRight(out, "\n"), "\n")
		}
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
}

// pagerPage is one screenful of pager lines at the current layout.
func (m *Model) pagerPage() int {
	_, _, contentH := m.dims()
	if p := contentH - 5; p > 1 {
		return p
	}
	return 1
}

// pagerKey scrolls the overlay; esc or q closes it, back to whatever was underneath.
func (m *Model) pagerKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "esc", "q":
		m.pagerOn = false
	case "j", "down", "enter":
		m.pagerOff++
	case "k", "up":
		m.pagerOff--
	case " ", "f":
		m.pagerOff += m.pagerPage()
	case "b":
		m.pagerOff -= m.pagerPage()
	case "g":
		m.pagerOff = 0
	case "G":
		m.pagerOff = len(m.pagerLines)
	}
}

// renderPager draws the pager overlay in place of the tiled area, clamping the offset.
func (m *Model) renderPager(w, h int) string {
	visible := h - 5
	if visible < 1 {
		visible = 1
	}
	maxOff := len(m.pagerLines) - visible
	if maxOff < 0 {
		maxOff = 0
	}
	if m.pagerOff > maxOff {
		m.pagerOff = maxOff
	}
	if m.pagerOff < 0 {
		m.pagerOff = 0
	}
	end := m.pagerOff + visible
	if end > len(m.pagerLines) {
		end = len(m.pagerLines)
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(m.th.accent).Render(ansi.Truncate(m.pagerTitle, w-4, "…")) + "\n\n")
	for _, l := range m.pagerLines[m.pagerOff:end] {
		b.WriteString(ansi.Truncate(l, w-4, "…") + "\n")
	}
	pos := "all"
	if maxOff > 0 {
		pos = fmt.Sprintf("%d%%", m.pagerOff*100/maxOff)
	}
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("j/k · space/b page · g/G ends · esc closes  ("+pos+")"))
	if w < 6 || h < 5 {
		return truncate(b.String(), w*h)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(m.th.accent).
		Width(w - 2).Height(h - 2).MaxHeight(h).
		Render(b.String())
}
