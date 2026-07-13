// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func writeBrief(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderMarkdownFallback(t *testing.T) {
	if got := renderMarkdown(filepath.Join(t.TempDir(), "missing.md"), 60); len(got) != 1 || !strings.Contains(got[0], "cannot read") {
		t.Fatalf("missing file = %q", got)
	}
	p := writeBrief(t, "# Title\n\nbody text\n")
	joined := strings.Join(renderMarkdown(p, 60), "\n")
	if !strings.Contains(joined, "Title") || !strings.Contains(joined, "body text") {
		t.Fatalf("rendered = %q", joined)
	}
}

func TestGateViewOpensPagerAndGateStays(t *testing.T) {
	m := newTestModel(startCatPanes(t, "boss", "worker"))
	brief := writeBrief(t, "# The Plan\n\ndo the thing\n")
	m.gates = []pendingGate{{cmd: ipc.Command{Cmd: "delegate", To: []string{"worker"}, Brief: brief}, to: "worker", at: time.Now()}}
	m.handleKey(key("v"))
	if !m.pagerOn || len(m.gates) != 1 {
		t.Fatalf("pagerOn=%v gates=%d, want pager open and gate pending", m.pagerOn, len(m.gates))
	}
	if v := m.View(); !strings.Contains(v, "The Plan") {
		t.Fatalf("pager view missing brief content:\n%s", v)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.pagerOn || len(m.gates) != 1 {
		t.Fatalf("esc: pagerOn=%v gates=%d, want gate overlay back", m.pagerOn, len(m.gates))
	}
	if v := m.View(); !strings.Contains(v, "awaiting approval") {
		t.Fatalf("gate overlay not restored:\n%s", v)
	}
}

func TestBoardSelectAndView(t *testing.T) {
	m := newTestModel(startCatPanes(t, "boss", "worker"))
	report := writeBrief(t, "# Report\n\nall good\n")
	m.board = []taskEvent{
		{at: time.Now(), kind: "delegate", id: "T1", to: "worker", task: "first"},
		{at: time.Now(), kind: "work-done", id: "T1", to: "worker", task: "done", file: report},
	}
	m.boardOn = true
	m.boardSel = len(m.board) - 1
	m.handleKey(key("k"))
	if !m.boardOn || m.boardSel != 0 {
		t.Fatalf("k: boardOn=%v sel=%d", m.boardOn, m.boardSel)
	}
	m.handleKey(key("v")) // selected entry has no file: board stays, no pager
	if m.pagerOn || !m.boardOn {
		t.Fatalf("v on file-less entry: pagerOn=%v boardOn=%v", m.pagerOn, m.boardOn)
	}
	m.handleKey(key("j"))
	m.handleKey(key("v"))
	if !m.pagerOn {
		t.Fatal("v on report entry did not open the pager")
	}
	if v := m.View(); !strings.Contains(v, "all good") {
		t.Fatalf("pager view missing report content:\n%s", v)
	}
	m.handleKey(key("q"))
	m.handleKey(key("x")) // any other key closes the board
	if m.pagerOn || m.boardOn {
		t.Fatalf("close: pagerOn=%v boardOn=%v", m.pagerOn, m.boardOn)
	}
}

func TestPagerScrollClamps(t *testing.T) {
	m := newTestModel(startCatPanes(t, "solo"))
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "line"
	}
	m.pagerOn, m.pagerLines = true, lines
	m.handleKey(key("k"))
	_ = m.View()
	if m.pagerOff != 0 {
		t.Fatalf("scroll above top: off=%d", m.pagerOff)
	}
	m.handleKey(key("G"))
	_ = m.View()
	if m.pagerOff <= 0 || m.pagerOff >= len(lines) {
		t.Fatalf("G: off=%d, want clamped to max", m.pagerOff)
	}
	bottom := m.pagerOff
	m.handleKey(tea.KeyMsg{Type: tea.KeyPgDown})
	_ = m.View()
	if m.pagerOff != bottom {
		t.Fatalf("PgDn past bottom moved: off=%d", m.pagerOff)
	}
	m.handleKey(key("g"))
	if m.pagerOff != 0 {
		t.Fatalf("g: off=%d", m.pagerOff)
	}
}
