// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/pane"
)

// capture collects pane input across goroutines (injectLine types Enter from a timer).
type capture struct {
	mu sync.Mutex
	b  []byte
}

func (c *capture) add(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.b = append(c.b, b...)
	return nil
}

func (c *capture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.b)
}

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
	got := &capture{}
	e := &entry{role: config.Role{Name: "coder", Worktree: true},
		pane: pane.Remote(80, 24, got.add, func(int, int) {})}
	orc := remoteEntry("orc", true)
	s := &session{cfg: config.Config{Roles: []config.Role{orc.role, e.role}}, panes: []*entry{orc, e}}
	s.deliverDelegate(e, 1, ipc.Command{Task: "do it", Brief: "briefs/b.md"})
	want := ctxPath(e.role, "worker-task-coder.md")
	if !filepath.IsAbs(want) || !strings.Contains(got.text(), "Read "+want) {
		t.Fatalf("injected task path must be absolute:\n%s", got.text())
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

func TestPlanAndPerformMerge(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	s := &session{cfg: config.Config{Roles: []config.Role{
		{Name: "orc", Start: true},
		{Name: "coder", Worktree: true, Merge: "gate"},
		{Name: "qa", OwnsFiles: []string{"defects.md"}},
	}}}
	if _, err := ensureWorktree("coder"); err != nil {
		t.Fatal(err)
	}
	if p, err := s.planMerge("coder"); err != nil || p != nil {
		t.Fatalf("fresh branch: plan=%v err=%v", p, err)
	}
	if err := os.WriteFile(filepath.Join(WorktreePath("coder"), "feat.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitWorktree("coder", "T1", "feature"); err != nil {
		t.Fatal(err)
	}
	p, err := s.planMerge("coder")
	if err != nil || p == nil {
		t.Fatalf("plan: %v %v", p, err)
	}
	if !strings.Contains(p.diffstat, "1 file") || len(p.ownedHit) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	if body, err := os.ReadFile(p.diff); err != nil || !strings.Contains(string(body), "+payload") {
		t.Fatalf("diff file: %v %s", err, body)
	}
	// a touched owned file is flagged
	if err := os.WriteFile(filepath.Join(WorktreePath("coder"), "defects.md"), []byte("sneaky"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitWorktree("coder", "T2", "sneaky"); err != nil {
		t.Fatal(err)
	}
	if p, err = s.planMerge("coder"); err != nil || len(p.ownedHit) != 1 || p.ownedHit[0] != "defects.md" {
		t.Fatalf("owned hit: %+v %v", p, err)
	}
	// uncommitted tracked work in the main tree refuses
	if err := os.WriteFile("f.txt", []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, reason := performMerge("coder", "T2"); !strings.Contains(reason, "uncommitted") {
		t.Fatalf("dirty tree must refuse: %q", reason)
	}
	gitT(t, "checkout", "--", "f.txt")
	// the merge lands with the choragos subject
	sha, reason := performMerge("coder", "T2")
	if reason != "" || sha == "" {
		t.Fatalf("merge: %q %q", sha, reason)
	}
	if subj := gitT(t, "log", "-1", "--format=%s"); subj != "choragos: merge coder (T2)" {
		t.Fatalf("subject = %q", subj)
	}
	if _, err := os.Stat("feat.txt"); err != nil {
		t.Fatal("merged file must reach the main tree")
	}
	// a conflict aborts cleanly and names the path
	if err := os.WriteFile(filepath.Join(WorktreePath("coder"), "f.txt"), []byte("branch side"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitWorktree("coder", "T3", "branch side"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("f.txt", []byte("main side"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, "add", "f.txt")
	gitT(t, "commit", "-q", "-m", "main side")
	if _, reason := performMerge("coder", "T3"); !strings.Contains(reason, "conflicts in: f.txt") {
		t.Fatalf("conflict must name the path: %q", reason)
	}
	if _, err := os.Stat(filepath.Join(".git", "MERGE_HEAD")); err == nil {
		t.Fatal("aborted merge must leave no MERGE_HEAD")
	}
}

func TestQueueMergeModesAndGate(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	orcGot := &capture{}
	orc := &entry{role: config.Role{Name: "orc", Start: true},
		pane: pane.Remote(80, 24, orcGot.add, func(int, int) {})}
	drop := func([]byte) error { return nil }
	gate := &entry{role: config.Role{Name: "coder", Worktree: true, Merge: "gate"}, pane: pane.Remote(80, 24, drop, func(int, int) {})}
	auto := &entry{role: config.Role{Name: "bot", Worktree: true, Merge: "auto"}, pane: pane.Remote(80, 24, drop, func(int, int) {})}
	manual := &entry{role: config.Role{Name: "lone", Worktree: true}, pane: pane.Remote(80, 24, drop, func(int, int) {})}
	s := &session{cfg: config.Config{Roles: []config.Role{orc.role, gate.role, auto.role, manual.role, {Name: "qa", OwnsFiles: []string{"defects.md"}}}},
		panes: []*entry{orc, gate, auto, manual}}
	work := func(role, id, file string) {
		t.Helper()
		if _, err := ensureWorktree(role); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(WorktreePath(role), file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := commitWorktree(role, id, file); err != nil {
			t.Fatal(err)
		}
	}
	// manual and unknown roles are no-ops
	work("lone", "T0", "lone.txt")
	s.queueMerge("lone", "T0")
	s.queueMerge("ghost", "T0")
	if len(s.gates) != 0 {
		t.Fatalf("manual mode must not gate: %+v", s.gates)
	}
	// gate mode holds the diff, approve lands it
	work("coder", "T1", "one.txt")
	s.queueMerge("coder", "T1")
	if len(s.gates) != 1 || s.gates[0].mergeID != "T1" || s.gates[0].report == "" {
		t.Fatalf("gate expected: %+v", s.gates)
	}
	s.approveGate()
	if _, err := os.Stat("one.txt"); err != nil {
		t.Fatal("approved merge must land")
	}
	if !strings.Contains(orcGot.text(), "Merged coder's branch for task T1") {
		t.Fatalf("orchestrator must hear the merge:\n%s", orcGot.text())
	}
	// reject keeps the branch
	work("coder", "T2", "two.txt")
	s.queueMerge("coder", "T2")
	s.rejectGate()
	if _, err := os.Stat("two.txt"); err == nil {
		t.Fatal("rejected merge must not land")
	}
	// auto mode merges without a gate
	work("bot", "T3", "three.txt")
	s.queueMerge("bot", "T3")
	if len(s.gates) != 0 {
		t.Fatalf("auto mode must not gate: %+v", s.gates)
	}
	if _, err := os.Stat("three.txt"); err != nil {
		t.Fatal("auto merge must land")
	}
	// an owned-file hit gates even in auto mode
	work("bot", "T4", "defects.md")
	s.queueMerge("bot", "T4")
	if len(s.gates) != 1 || !strings.Contains(s.gates[0].reason, "owned files: defects.md") {
		t.Fatalf("owned hit must gate: %+v", s.gates)
	}
	s.rejectGate()
	// approve on a now-dirty main tree fails closed and keeps the branch
	work("coder", "T5", "five.txt")
	s.queueMerge("coder", "T5")
	if err := os.WriteFile("f.txt", []byte("dirty again"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.approveGate()
	if _, err := os.Stat("five.txt"); err == nil {
		t.Fatal("failed merge must not land")
	}
	if !strings.Contains(orcGot.text(), "failed: the main tree has uncommitted changes") {
		t.Fatalf("orchestrator must hear the failure:\n%s", orcGot.text())
	}
}

func TestPlanMergeFailureBranches(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	s := &session{cfg: config.Config{Roles: []config.Role{{Name: "coder", Worktree: true}}}}
	// put the branch one commit ahead of HEAD so the plan proceeds
	gitT(t, "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "ahead")
	gitT(t, "branch", WorktreeBranch("coder"))
	gitT(t, "reset", "-q", "--hard", "HEAD~1")
	// a plain file where contextDir should be blocks the diff write
	if err := os.WriteFile(contextDir, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.planMerge("coder"); err == nil {
		t.Fatal("blocked contextDir must surface")
	}
	if err := os.Remove(contextDir); err != nil {
		t.Fatal(err)
	}
	// a directory where the diff file goes blocks the write
	if err := os.MkdirAll(filepath.Join(contextDir, "merge-coder.diff"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.planMerge("coder"); err == nil {
		t.Fatal("blocked diff path must surface")
	}
	// outside a repo, plan and perform surface git's errors
	t.Chdir(t.TempDir())
	if _, err := s.planMerge("coder"); err == nil {
		t.Fatal("outside a repo the plan must error")
	}
	if _, reason := performMerge("coder", "T1"); reason == "" {
		t.Fatal("outside a repo the merge must refuse")
	}
}

func TestPerformMergeUnmergeableBranch(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	if _, reason := performMerge("ghost", "T1"); reason == "" || strings.Contains(reason, "conflicts") {
		t.Fatalf("a missing branch must refuse without conflicts: %q", reason)
	}
}

func TestQueueMergeEdges(t *testing.T) {
	t.Chdir(t.TempDir())
	initRepo(t)
	drop := func([]byte) error { return nil }
	bot := &entry{role: config.Role{Name: "bot", Worktree: true, Merge: "auto"}, pane: pane.Remote(80, 24, drop, func(int, int) {})}
	rang := false
	s := &session{cfg: config.Config{Roles: []config.Role{bot.role}}, panes: []*entry{bot}, bellFn: func() { rang = true }}
	// a freshly created (already merged) branch skips
	if _, err := ensureWorktree("bot"); err != nil {
		t.Fatal(err)
	}
	s.queueMerge("bot", "T1")
	if len(s.gates) != 0 {
		t.Fatalf("merged branch must skip: %+v", s.gates)
	}
	// an auto merge refused by a dirty main tree falls closed to a gate and rings
	if err := os.WriteFile(filepath.Join(WorktreePath("bot"), "auto.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitWorktree("bot", "T2", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("f.txt", []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.queueMerge("bot", "T2")
	if len(s.gates) != 1 || !strings.Contains(s.gates[0].reason, "auto-merge refused") || !rang {
		t.Fatalf("refused auto merge must gate and ring: %+v rang=%v", s.gates, rang)
	}
	gitT(t, "checkout", "--", "f.txt")
	// a broken repo state surfaces as a plan warning, not a gate
	s.gates = nil
	t.Chdir(t.TempDir())
	s.queueMerge("bot", "T3")
	if len(s.gates) != 0 {
		t.Fatalf("plan failure must not gate: %+v", s.gates)
	}
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
