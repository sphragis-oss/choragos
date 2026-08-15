// SPDX-License-Identifier: Apache-2.0

// Per-role git worktrees: a worktree role spawns in its own checkout under
// .choragos/worktrees/<role> on branch choragos/<role>, so parallel roles
// never trample each other (see docs/design-role-worktrees.md).
package deck

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
)

// WorktreePath is the role's checkout directory under contextDir.
func WorktreePath(role string) string {
	return filepath.Join(contextDir, "worktrees", sanitize(role))
}

// WorktreeBranch is the role's per-repo branch, reused across sessions.
func WorktreeBranch(role string) string {
	return "choragos/" + sanitize(role)
}

// runGit runs git in dir and returns trimmed combined output.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// worktreePreflight refuses startup when a worktree role cannot get isolation.
func worktreePreflight(roles []config.Role) error {
	var need []string
	for _, r := range roles {
		if r.Worktree {
			need = append(need, r.Name)
		}
	}
	if len(need) == 0 {
		return nil
	}
	who := strings.Join(need, ", ")
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("worktree role(s) %s need git in PATH: %w", who, err)
	}
	if _, err := runGit(".", "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("worktree role(s) %s need a git repository: %w", who, err)
	}
	if _, err := runGit(".", "rev-parse", "HEAD"); err != nil {
		return fmt.Errorf("worktree role(s) %s need at least one commit: %w", who, err)
	}
	return nil
}

// ensureWorktree creates or reuses the role's worktree; a live checkout is never reset.
func ensureWorktree(role string) (string, error) {
	path := WorktreePath(role)
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return path, nil // uncommitted agent work stays untouched
	}
	// a deleted checkout leaves a stale admin entry that blocks the re-add
	if _, err := runGit(".", "worktree", "prune"); err != nil {
		return "", err
	}
	branch := WorktreeBranch(role)
	if _, err := runGit(".", "rev-parse", "--verify", "refs/heads/"+branch); err != nil {
		if _, err := runGit(".", "worktree", "add", "-b", branch, path, "HEAD"); err != nil {
			return "", err
		}
		return path, nil
	}
	// fast-forward a fully merged branch; unmerged work is kept as is
	if _, err := runGit(".", "merge-base", "--is-ancestor", branch, "HEAD"); err == nil {
		if _, err := runGit(".", "branch", "-f", branch, "HEAD"); err != nil {
			return "", err
		}
	}
	if _, err := runGit(".", "worktree", "add", path, branch); err != nil {
		return "", err
	}
	return path, nil
}

// maybeCommitWorktree lands a worktree role's work-done as a branch commit; failure warns, never blocks.
func (s *session) maybeCommitWorktree(cmd ipc.Command) {
	role := s.delegateRole(cmd.ID)
	if role == "" {
		return
	}
	e, _ := s.findRole(role)
	if e == nil || !e.role.Worktree {
		return
	}
	sha, err := commitWorktree(role, cmd.ID, singleLine(cmd.Task))
	switch {
	case err != nil:
		s.log().Warn("worktree commit failed", "task", cmd.ID, "role", role, "err", err)
	case sha == "":
		s.log().Info("worktree clean, nothing to commit", "task", cmd.ID, "role", role)
	default:
		s.log().Info("worktree commit", "task", cmd.ID, "role", role, "sha", sha)
	}
}

// commitWorktree records the role's current work as one commit on its branch; "" when clean.
func commitWorktree(role, id, label string) (string, error) {
	dir := WorktreePath(role)
	status, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if status == "" {
		return "", nil
	}
	if _, err := runGit(dir, "add", "-A"); err != nil {
		return "", err
	}
	subject := strings.TrimSpace("choragos: " + id + " " + label)
	if _, err := runGit(dir, "-c", "user.name=choragos", "-c", "user.email=choragos@localhost",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", subject); err != nil {
		return "", err
	}
	return runGit(dir, "rev-parse", "--short", "HEAD")
}

// mergePlan describes what merging the role branch would land.
type mergePlan struct {
	branch   string
	diffstat string   // git diff --stat summary line
	diff     string   // path to the written full diff
	ownedHit []string // touched paths some other role owns
}

// planMerge inspects the role branch against HEAD; nil when there is nothing to merge.
func (s *session) planMerge(role string) (*mergePlan, error) {
	branch := WorktreeBranch(role)
	if _, err := runGit(".", "merge-base", "--is-ancestor", branch, "HEAD"); err == nil {
		return nil, nil
	}
	stat, err := runGit(".", "diff", "--stat", "HEAD..."+branch)
	if err != nil {
		return nil, err
	}
	full, err := runGit(".", "diff", "HEAD..."+branch)
	if err != nil {
		return nil, err
	}
	diffFile := filepath.Join(contextDir, "merge-"+sanitize(role)+".diff")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(diffFile, []byte(full+"\n"), 0o644); err != nil {
		return nil, err
	}
	p := &mergePlan{branch: branch, diffstat: statSummary(stat), diff: diffFile}
	names, err := runGit(".", "diff", "--name-only", "HEAD..."+branch)
	if err != nil {
		return nil, err
	}
	owned := s.cfg.OwnedFiles()
	for _, n := range strings.Fields(names) {
		if owner := owned[n]; owner != "" && owner != role {
			p.ownedHit = append(p.ownedHit, n)
		}
	}
	return p, nil
}

// statSummary keeps the closing "N files changed ..." line of a --stat block.
func statSummary(stat string) string {
	lines := strings.Split(strings.TrimSpace(stat), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// performMerge lands the role branch on the current branch; a non-empty reason means it refused cleanly.
func performMerge(role, id string) (sha, reason string) {
	status, err := runGit(".", "status", "--porcelain")
	if err != nil {
		return "", err.Error()
	}
	for _, l := range strings.Split(status, "\n") {
		// untracked files (.choragos itself) never block; uncommitted tracked work does
		if l != "" && !strings.HasPrefix(l, "??") {
			return "", "the main tree has uncommitted changes; commit or stash them first"
		}
	}
	branch := WorktreeBranch(role)
	if _, err := runGit(".", "-c", "user.name=choragos", "-c", "user.email=choragos@localhost",
		"-c", "commit.gpgsign=false", "merge", "--no-ff", "-m", "choragos: merge "+role+" ("+id+")", branch); err != nil {
		conflicts, _ := runGit(".", "diff", "--name-only", "--diff-filter=U")
		_, _ = runGit(".", "merge", "--abort")
		if conflicts != "" {
			return "", "conflicts in: " + strings.Join(strings.Fields(conflicts), ", ")
		}
		return "", err.Error()
	}
	out, err := runGit(".", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err.Error()
	}
	return out, ""
}

// queueMerge runs the role's merge mode after an accepted work-done.
func (s *session) queueMerge(role, id string) {
	e, _ := s.findRole(role)
	if e == nil || !e.role.Worktree || e.role.MergeMode() == "manual" {
		return
	}
	plan, err := s.planMerge(role)
	if err != nil {
		s.log().Warn("merge plan failed", "role", role, "task", id, "err", err)
		return
	}
	if plan == nil {
		s.log().Info("merge skipped, branch already merged", "role", role, "task", id)
		return
	}
	reason := "merge " + plan.branch + ": " + plan.diffstat + " (task " + id + ")"
	switch {
	case len(plan.ownedHit) > 0:
		// a diff into another role's owned files always faces a human, whatever the mode
		s.gateMerge(role, id, reason+"; touches owned files: "+strings.Join(plan.ownedHit, ", "), plan.diff)
	case e.role.MergeMode() == "auto":
		s.snapshotMerge(id, role)
		if sha, fail := performMerge(role, id); fail == "" {
			s.log().Info("merged", "role", role, "task", id, "sha", sha)
			s.notifyOrchestrator("[choragos] Merged " + role + "'s branch for task " + id + " (" + sha + ").")
		} else {
			s.gateMerge(role, id, reason+"; auto-merge refused: "+fail, plan.diff)
		}
	default:
		s.gateMerge(role, id, reason, plan.diff)
	}
}

// gateMerge holds the diff for a human, mirroring the ownership gate's queue mechanics.
func (s *session) gateMerge(role, id, reason, diff string) {
	s.gates = append(s.gates, pendingGate{to: role, at: time.Now(), reason: reason, report: diff, mergeID: id})
	s.saveSnapshot()
	s.log().Info("merge gate", "role", role, "task", id, "reason", reason)
	if s.bellFn != nil {
		s.bellFn()
	}
	s.runHook(s.cfg.UI.OnGate, role, reason)
}

// resolveMerge closes a merge gate: approve lands the branch, reject keeps it.
func (s *session) resolveMerge(g pendingGate, accept bool) {
	s.log().Info("merge gate resolved", "to", g.to, "task", g.mergeID, "accepted", accept)
	if !accept {
		s.notifyOrchestrator("[choragos] The user declined merging " + g.to + "'s branch for task " + g.mergeID + "; the branch is kept as is.")
		return
	}
	s.snapshotMerge(g.mergeID, g.to)
	sha, fail := performMerge(g.to, g.mergeID)
	if fail != "" {
		s.log().Warn("merge failed", "role", g.to, "task", g.mergeID, "reason", fail)
		s.notifyOrchestrator("[choragos] Merging " + g.to + "'s branch for task " + g.mergeID + " failed: " + fail + ". The branch is kept; resolve it with git and merge by hand.")
		return
	}
	s.log().Info("merged", "role", g.to, "task", g.mergeID, "sha", sha)
	s.notifyOrchestrator("[choragos] Merged " + g.to + "'s branch for task " + g.mergeID + " (" + sha + ").")
}

// ctxPath is the injected path for a context file; absolute for worktree roles, whose cwd differs.
func ctxPath(r config.Role, name string) string {
	p := filepath.Join(contextDir, name)
	if r.Worktree {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	return p
}

// ownedFor is the ownership map as the role should address it; absolute for worktree roles.
func ownedFor(r config.Role, owned map[string]string) map[string]string {
	if !r.Worktree || len(owned) == 0 {
		return owned
	}
	m := make(map[string]string, len(owned))
	for p, o := range owned {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		m[p] = o
	}
	return m
}
