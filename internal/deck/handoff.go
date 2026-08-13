// SPDX-License-Identifier: Apache-2.0

// Session handoff: a deliberate session boundary. The orchestrator writes a
// handoff document, the deck quits, and the next serve --resume (optionally
// with a new team config) attaches it to the new orchestrator's boot context.
package deck

import (
	"cmp"
	"os"
	"path/filepath"
	"time"
)

// handoffFile is the agent-authored session handoff under contextDir.
const handoffFile = "handoff-session.md"

// handoffTimeout bounds the wait for the document; the deck quits either way.
const handoffTimeout = 120 * time.Second

// startHandoff asks the orchestrator for the handoff document; the tick loops watch for it.
func (s *session) startHandoff(nextConfig string) {
	if !s.handoffAt.IsZero() {
		return
	}
	s.handoffAt = time.Now()
	s.handoffCfg = nextConfig
	s.log().Info("handoff requested", "next_config", cmp.Or(nextConfig, s.cfg.Path))
	s.injectOrchestrator("[choragos] The user requested a session handoff. First append a short dated entry to " +
		filepath.Join(contextDir, memoryFile) +
		" with decisions, gotchas, and conventions from this session worth keeping for every future one. Then write " +
		filepath.Join(contextDir, handoffFile) +
		" covering the goal, the state of the work, in-flight and remaining tasks, and anything the next team must know. The session ends once the handoff file is written (2 minute limit).")
}

// checkHandoff reports whether a pending handoff finished (document written or timed out) and the deck should quit.
func (s *session) checkHandoff() bool {
	if s.handoffAt.IsZero() {
		return false
	}
	fi, err := os.Stat(filepath.Join(contextDir, handoffFile))
	if err == nil && fi.ModTime().After(s.handoffAt) {
		s.log().Info("handoff document written; stopping for the next session")
		s.finishHandoff()
		return true
	}
	if time.Since(s.handoffAt) > handoffTimeout {
		s.log().Warn("handoff timed out without a document; stopping anyway")
		s.finishHandoff()
		return true
	}
	return false
}

// finishHandoff stamps the quit snapshot for the next session: its config, no stale layout.
func (s *session) finishHandoff() {
	s.handoffDone = true
	if s.handoffCfg != "" {
		s.cfg.Path = s.handoffCfg
	}
	s.layout = nil // the next team's layout is its own business
}

// handoffNote points a resumed orchestrator at the previous session's handoff document.
func (s *session) handoffNote() string {
	if s.resume == nil {
		return ""
	}
	path := filepath.Join(contextDir, handoffFile)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return "\n## Handoff from the previous session\n\nRead " + path + " before delegating: the previous orchestrator wrote it for you.\n"
}
