// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
)

func gitT(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T) {
	t.Helper()
	gitT(t, "init", "-q")
	gitT(t, "config", "user.email", "t@example.com")
	gitT(t, "config", "user.name", "t")
	if err := os.WriteFile("f.txt", []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, "add", "f.txt")
	gitT(t, "commit", "-q", "-m", "one")
}

func TestWorktreePreflight(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := worktreePreflight([]config.Role{{Name: "coder"}}); err != nil {
		t.Fatalf("no worktree roles: preflight must be a no-op: %v", err)
	}
	wt := []config.Role{{Name: "coder", Worktree: true}}
	if err := worktreePreflight(wt); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("outside a repo must refuse: %v", err)
	}
	gitT(t, "init", "-q")
	if err := worktreePreflight(wt); err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("empty repo must refuse: %v", err)
	}
	gitT(t, "config", "user.email", "t@example.com")
	gitT(t, "config", "user.name", "t")
	gitT(t, "commit", "-q", "--allow-empty", "-m", "one")
	if err := worktreePreflight(wt); err != nil {
		t.Fatalf("repo with a commit must pass: %v", err)
	}
}

func TestEnsureWorktreeLifecycle(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	dir, err := ensureWorktree("coder")
	if err != nil {
		t.Fatal(err)
	}
	if dir != WorktreePath("coder") {
		t.Fatalf("dir = %q", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "f.txt")); err != nil {
		t.Fatal("checkout must carry HEAD content")
	}
	// a live checkout is reused; uncommitted work is never reset
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again, err := ensureWorktree("coder"); err != nil || again != dir {
		t.Fatalf("reuse: %q %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wip.txt")); err != nil {
		t.Fatal("reuse must keep uncommitted work")
	}
	// merged branch + deleted checkout: recreated at the new HEAD
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("g.txt", []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, "add", "g.txt")
	gitT(t, "commit", "-q", "-m", "two")
	if _, err := ensureWorktree("coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "g.txt")); err != nil {
		t.Fatal("merged branch must fast-forward to HEAD")
	}
	// an unmerged branch keeps its tip across a deleted checkout
	gitT(t, "-C", dir, "commit", "-q", "--allow-empty", "-m", "agent work")
	tip := gitT(t, "rev-parse", "refs/heads/"+WorktreeBranch("coder"))
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWorktree("coder"); err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, "rev-parse", "refs/heads/"+WorktreeBranch("coder")); got != tip {
		t.Fatalf("unmerged branch must keep its tip: %s != %s", got, tip)
	}
	t.Chdir(t.TempDir())
	if _, err := ensureWorktree("coder"); err == nil {
		t.Fatal("outside a repo must error")
	}
}

func TestWorktreePreflightNoGit(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())
	if err := worktreePreflight([]config.Role{{Name: "c", Worktree: true}}); err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("missing git must refuse: %v", err)
	}
}

func TestEnsureWorktreeAddFailures(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	// a non-empty plain directory blocks the fresh add
	if err := os.MkdirAll(WorktreePath("coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(WorktreePath("coder"), "junk"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWorktree("coder"); err == nil {
		t.Fatal("blocked add must error")
	}
	// the failed add left the branch behind, so the retry takes the existing-branch path
	gitT(t, "rev-parse", "--verify", "refs/heads/"+WorktreeBranch("coder"))
	if _, err := ensureWorktree("coder"); err == nil {
		t.Fatal("blocked add on an existing branch must error")
	}
	// a merged branch held by a foreign checkout refuses the fast-forward
	gitT(t, "worktree", "add", "-q", "-b", WorktreeBranch("qa"), "elsewhere", "HEAD")
	if _, err := ensureWorktree("qa"); err == nil {
		t.Fatal("a branch checked out elsewhere must surface the failure")
	}
}

func TestCtxPathAndOwnedFor(t *testing.T) {
	t.Chdir(t.TempDir())
	plain := config.Role{Name: "a"}
	wt := config.Role{Name: "b", Worktree: true}
	if p := ctxPath(plain, "x.md"); filepath.IsAbs(p) {
		t.Fatalf("plain role path must stay relative: %q", p)
	}
	if p := ctxPath(wt, "x.md"); !filepath.IsAbs(p) || !strings.HasSuffix(p, filepath.Join(contextDir, "x.md")) {
		t.Fatalf("worktree role path must be absolute: %q", p)
	}
	owned := map[string]string{"defects.md": "qa"}
	if got := ownedFor(plain, owned); len(got) != 1 || got["defects.md"] != "qa" {
		t.Fatalf("plain role map must pass through: %v", got)
	}
	for p, o := range ownedFor(wt, owned) {
		if !filepath.IsAbs(p) || o != "qa" {
			t.Fatalf("worktree role map must be absolute: %q -> %q", p, o)
		}
	}
}

func TestDeliverDelegateWorktreeAbsolutePaths(t *testing.T) {
	t.Chdir(t.TempDir())
	var got []byte
	e := &entry{role: config.Role{Name: "coder", Worktree: true},
		pane: pane.Remote(80, 24, func(b []byte) error { got = append(got, b...); return nil }, func(int, int) {})}
	orc := remoteEntry("orc", true)
	s := &session{cfg: config.Config{Roles: []config.Role{orc.role, e.role}}, panes: []*entry{orc, e}}
	s.deliverDelegate(e, 1, ipc.Command{Task: "do it", Brief: "briefs/b.md"})
	want := ctxPath(e.role, "worker-task-coder.md")
	if !filepath.IsAbs(want) || !strings.Contains(string(got), "Read "+want) {
		t.Fatalf("injected task path must be absolute:\n%s", got)
	}
	body, err := os.ReadFile(filepath.Join(contextDir, "worker-task-coder.md"))
	if err != nil {
		t.Fatal(err)
	}
	absBrief, _ := filepath.Abs("briefs/b.md")
	if !strings.Contains(string(body), absBrief) {
		t.Fatalf("brief path must be absolute in the task file:\n%s", body)
	}
}

func TestCommitWorktree(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	if _, err := ensureWorktree("coder"); err != nil {
		t.Fatal(err)
	}
	if sha, err := commitWorktree("coder", "T1", "noop"); err != nil || sha != "" {
		t.Fatalf("clean tree: sha=%q err=%v", sha, err)
	}
	// new and modified files land as one commit with the task subject
	if err := os.WriteFile(filepath.Join(WorktreePath("coder"), "new.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(WorktreePath("coder"), "f.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, err := commitWorktree("coder", "T1", "did stuff")
	if err != nil || sha == "" {
		t.Fatalf("commit: sha=%q err=%v", sha, err)
	}
	if subj := gitT(t, "log", "-1", "--format=%s", WorktreeBranch("coder")); subj != "choragos: T1 did stuff" {
		t.Fatalf("subject = %q", subj)
	}
	if sha2, err := commitWorktree("coder", "T2", "noop"); err != nil || sha2 != "" {
		t.Fatalf("after the commit the tree is clean again: %q %v", sha2, err)
	}
	if _, err := commitWorktree("ghost", "T3", ""); err == nil {
		t.Fatal("missing worktree must error")
	}
	// a failing pre-commit hook surfaces the commit error (worktrees share .git/hooks)
	hook := filepath.Join(".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(WorktreePath("coder"), "hooked.txt"), []byte("h"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitWorktree("coder", "T4", "hooked"); err == nil {
		t.Fatal("failing pre-commit must surface")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	// an unreadable file surfaces the add failure
	bad := filepath.Join(WorktreePath("coder"), "unreadable.txt")
	if err := os.WriteFile(bad, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := commitWorktree("coder", "T5", "bad"); err == nil {
		t.Fatal("unreadable file must surface the add failure")
	}
	if err := os.Chmod(bad, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkDoneCommitsWorktree(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	drop := func([]byte) error { return nil }
	e := &entry{role: config.Role{Name: "coder", Worktree: true}, pane: pane.Remote(80, 24, drop, func(int, int) {})}
	plain := &entry{role: config.Role{Name: "scribe"}, pane: pane.Remote(80, 24, drop, func(int, int) {})}
	orc := remoteEntry("orc", true)
	s := &session{cfg: config.Config{Roles: []config.Role{orc.role, e.role, plain.role}}, panes: []*entry{orc, e, plain}}
	id := s.deliverDelegate(e, 1, ipc.Command{Task: "build it"})
	if _, err := ensureWorktree("coder"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(WorktreePath("coder"), "out.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.maybeCommitWorktree(ipc.Command{ID: id, Task: "built"})
	if subj := gitT(t, "log", "-1", "--format=%s", WorktreeBranch("coder")); subj != "choragos: "+id+" built" {
		t.Fatalf("subject = %q", subj)
	}
	// a now-clean tree logs and skips
	s.maybeCommitWorktree(ipc.Command{ID: id, Task: "noop"})
	// unknown ids and non-worktree roles are no-ops
	s.maybeCommitWorktree(ipc.Command{ID: "T99", Task: "x"})
	pid := s.deliverDelegate(plain, 2, ipc.Command{Task: "note it"})
	s.maybeCommitWorktree(ipc.Command{ID: pid, Task: "noted"})
	// a missing worktree takes the warn path without blocking
	if err := os.RemoveAll(WorktreePath("coder")); err != nil {
		t.Fatal(err)
	}
	s.maybeCommitWorktree(ipc.Command{ID: id, Task: "gone"})
}

func TestStartRoleSpawnsInWorktree(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	p, err := startRole(config.Role{Name: "coder", Command: "cat", Worktree: true}, 80, 24, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if _, err := os.Stat(filepath.Join(WorktreePath("coder"), ".git")); err != nil {
		t.Fatal("spawn must create the worktree")
	}
}

func TestStartRoleWorktreeErrorOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := startRole(config.Role{Name: "coder", Command: "cat", Worktree: true}, 80, 24, os.Environ()); err == nil || !strings.Contains(err.Error(), "worktree for role") {
		t.Fatalf("startRole must surface the worktree failure: %v", err)
	}
}

func TestSessionStartRefusesWorktreeOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	s := &session{cfg: config.Config{Roles: []config.Role{{Name: "w", Command: "cat", Worktree: true}}}}
	if err := s.start(80, 24); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("start must refuse worktree roles outside a repo: %v", err)
	}
}
