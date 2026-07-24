// SPDX-License-Identifier: Apache-2.0

// Write-ownership tripwire: owned files are hashed at delegation delivery and
// re-checked at work-done; a non-owner change fails closed to a human gate.
// This is detection with best-effort attribution, not OS-level prevention
// (see docs/design-write-ownership.md).
package deck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

// ownAbsent and ownUnreadable are hash sentinels; unreadable always compares as changed.
const (
	ownAbsent     = "absent"
	ownUnreadable = "unreadable"
)

// hashOwnedFile returns a content fingerprint; creation, deletion, and read errors all read as change.
func hashOwnedFile(path string) string {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ownAbsent
	}
	if err != nil {
		return ownUnreadable
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// snapshotOwned records the owned files' state for this delegation; no-op without ownership claims.
func (s *session) snapshotOwned(id string) {
	owned := s.cfg.OwnedFiles()
	if len(owned) == 0 || id == "" {
		return
	}
	if s.ownSnaps == nil {
		s.ownSnaps = make(map[string]map[string]string)
	}
	snap := make(map[string]string, len(owned))
	for p := range owned {
		snap[p] = hashOwnedFile(p)
	}
	s.ownSnaps[id] = snap
}

// checkOwned compares the task's snapshot against the workspace and returns the
// owned files a non-owner delegation changed, split by whether the owner also
// had an in-flight delegation (ambiguous attribution).
func (s *session) checkOwned(id, role string) (violated, ambiguous []string) {
	snap, ok := s.ownSnaps[id]
	if !ok {
		return nil, nil
	}
	delete(s.ownSnaps, id)
	owned := s.cfg.OwnedFiles()
	for p, before := range snap {
		owner := owned[p]
		if owner == "" || owner == role {
			continue // unclaimed since snapshot, or the owner's own delegation
		}
		after := hashOwnedFile(p)
		if before == after && before != ownUnreadable && after != ownUnreadable {
			continue
		}
		if s.ownerActive(owner, id) {
			ambiguous = append(ambiguous, p)
			continue
		}
		violated = append(violated, p)
	}
	sort.Strings(violated)
	sort.Strings(ambiguous)
	return violated, ambiguous
}

// ownerActive reports whether the owner role has another delegation still in flight.
func (s *session) ownerActive(owner, excludeID string) bool {
	for i := len(s.board) - 1; i >= 0; i-- {
		ev := s.board[i]
		if ev.kind == "delegate" && ev.to == owner && ev.id != excludeID && ev.doneAt.IsZero() && !ev.timedOut {
			return true
		}
	}
	return false
}

// delegateRole resolves a work-done id back to the role it was delegated to.
func (s *session) delegateRole(id string) string {
	if id == "" {
		return ""
	}
	for i := len(s.board) - 1; i >= 0; i-- {
		if s.board[i].kind == "delegate" && s.board[i].id == id {
			return s.board[i].to
		}
	}
	return ""
}

// ownershipReason runs the check, logs it, warns on ambiguity, and returns the
// violation reason; empty means the delegation left owned files alone.
func (s *session) ownershipReason(id, role string) string {
	violated, ambiguous := s.checkOwned(id, role)
	if len(ambiguous) > 0 {
		s.log().Warn("ownership", "id", id, "role", role, "verdict", "ambiguous", "files", strings.Join(ambiguous, ","))
		s.notifyOrchestrator(fmt.Sprintf("[choragos] Warning: owned file(s) %s changed while %s and their owner both had tasks in flight; attribution is ambiguous. Verify with the owner.",
			strings.Join(ambiguous, ", "), role))
	}
	if len(violated) == 0 {
		return ""
	}
	owned := s.cfg.OwnedFiles()
	names := make([]string, len(violated))
	for i, p := range violated {
		names[i] = p + " (owned by " + owned[p] + ")"
	}
	s.log().Warn("ownership", "id", id, "role", role, "verdict", "violation", "files", strings.Join(violated, ","))
	return "ownership violation: " + role + " changed " + strings.Join(names, ", ")
}

// gateOwnership holds a plain work-done at a human gate; the orchestrator hears it only after the user rules.
func (s *session) gateOwnership(cmd ipc.Command, role, reason string) {
	s.gates = append(s.gates, pendingGate{cmd: cmd, to: role, at: time.Now(), reason: reason, report: cmd.Report, ownership: true})
	if s.bellFn != nil {
		s.bellFn()
	}
	s.runHook(s.cfg.UI.OnGate, role, reason)
}

// resolveOwnership closes an ownership gate: accept forwards the held report, reject asks for a redo.
func (s *session) resolveOwnership(g pendingGate, accept bool) {
	s.log().Info("ownership gate resolved", "to", g.to, "accepted", accept, "reason", g.reason)
	if accept {
		msg := fmt.Sprintf("[choragos] The user accepted %s's work despite an %s.", g.to, g.reason)
		if g.report != "" {
			msg += " Report: read " + g.report
		}
		s.notifyOrchestrator(msg)
		return
	}
	s.notifyOrchestrator(fmt.Sprintf("[choragos] The user rejected %s's work: %s. Route changes to owned files through their owner and delegate again.", g.to, g.reason))
}
