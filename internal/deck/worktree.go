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

	"github.com/sphragis-oss/choragos/internal/config"
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
