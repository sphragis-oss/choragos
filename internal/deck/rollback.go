// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sphragis-oss/choragos/internal/checkpoint"
)

// startRollback opens the confirm overlay for the selected board entry's checkpoint.
func (m *Model) startRollback() {
	if m.boardSel < 0 || m.boardSel >= len(m.board) {
		return
	}
	ev := m.board[m.boardSel]
	m.rbOn, m.rbStore, m.rbMsg, m.rbWarn = true, nil, "", ""
	if ev.id == "" {
		m.rbMsg = "this row has no task id to roll back to"
		return
	}
	st := checkpoint.New(".")
	if ok, reason := st.Active(); !ok {
		m.rbMsg = "checkpoints unavailable: " + reason
		return
	}
	entries, err := st.List()
	if err != nil {
		m.rbMsg = err.Error()
		return
	}
	var target checkpoint.Entry
	found := false
	for _, e := range entries { // newest first
		if e.TaskID == ev.id {
			target, found = e, true
			break
		}
	}
	if !found {
		m.rbMsg = "no checkpoint for " + ev.id + " (pruned, or taken by an older session)"
		return
	}
	// snapshot the current state first: the rollback itself stays undoable
	pre, err := st.Snapshot(fmt.Sprintf("%d-pre-rollback-%s", time.Now().Unix(), target.TaskID),
		"pre-rollback -> "+target.Name, "head: "+st.Head())
	if err != nil {
		m.rbMsg = err.Error()
		return
	}
	changed, extra, err := st.Diff(target.Ref, pre)
	if err != nil {
		m.rbMsg = err.Error()
		return
	}
	if len(changed) == 0 {
		m.rbMsg = "workspace already matches the checkpoint for " + ev.id
		return
	}
	if ev.kind == "delegate" && ev.doneAt.IsZero() {
		m.rbWarn = "task not reported done; its worker may still be writing"
	}
	m.rbStore, m.rbTarget, m.rbExtra, m.rbFiles = st, target, extra, len(changed)-len(extra)
}

// rollbackKey applies on y; any other key closes the overlay (back to the board).
func (m *Model) rollbackKey(msg tea.KeyMsg) {
	if m.rbMsg != "" || m.rbStore == nil {
		m.closeRollback()
		return
	}
	if msg.Type == tea.KeyRunes && (string(msg.Runes) == "y" || string(msg.Runes) == "Y") {
		if err := m.rbStore.Apply(m.rbTarget.Ref, m.rbExtra); err != nil {
			m.rbMsg = "rollback failed: " + err.Error()
			m.rbStore = nil
			return
		}
		m.log().Info("rollback", "task", m.rbTarget.TaskID, "ref", m.rbTarget.Ref,
			"restored", m.rbFiles, "deleted", len(m.rbExtra))
		m.rbMsg = fmt.Sprintf("rolled back to %s: %d file(s) restored, %d deleted\nundo with: choragos rollback pre-rollback-%s",
			m.rbTarget.Name, m.rbFiles, len(m.rbExtra), m.rbTarget.TaskID)
		m.rbStore = nil // result showing; the next key closes
		return
	}
	m.closeRollback()
}

func (m *Model) closeRollback() {
	m.rbOn, m.rbStore, m.rbMsg, m.rbWarn = false, nil, "", ""
}

// renderRollback draws the confirm (or result) overlay; history is never touched, only files.
func (m *Model) renderRollback(w, h int) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(m.th.waiting).Render("workspace rollback") + "\n\n")
	if m.rbMsg != "" {
		b.WriteString(m.rbMsg + "\n\n")
		b.WriteString(lipgloss.NewStyle().Faint(true).Render("press any key to close"))
	} else {
		row := func(k, v string) {
			b.WriteString(lipgloss.NewStyle().Foreground(m.th.accent).Width(10).Render(k) + " " + v + "\n")
		}
		row("task", m.rbTarget.TaskID+"  "+truncate(m.rbTarget.Subject, w-18))
		row("taken", humanizeSince(time.Since(m.rbTarget.At)))
		row("effect", fmt.Sprintf("%d file(s) restored, %d deleted", m.rbFiles, len(m.rbExtra)))
		if m.rbWarn != "" {
			b.WriteString("\n" + lipgloss.NewStyle().Foreground(m.th.waiting).Render("warning: "+m.rbWarn) + "\n")
		}
		b.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render("[y] roll back   [any other key] cancel") + "\n")
		b.WriteString(lipgloss.NewStyle().Faint(true).Render("files only: HEAD, branches, and worker commits stay untouched; it is undoable"))
	}
	if w < 6 || h < 5 {
		return truncate(b.String(), w*h)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(m.th.waiting).
		Width(w - 2).Height(h - 2).MaxHeight(h).
		Render(b.String())
}
