// SPDX-License-Identifier: Apache-2.0

package checkpoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func initRepo(t *testing.T) (string, *Store) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	st := New(dir)
	if ok, reason := st.Active(); !ok {
		t.Fatalf("Active = false: %s", reason)
	}
	return dir, st
}

func TestActiveNonRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	ok, reason := New(t.TempDir()).Active()
	if ok || !strings.Contains(reason, "not a git repository") {
		t.Fatalf("Active = %v %q, want repo refusal", ok, reason)
	}
}

func TestSnapshotListPrune(t *testing.T) {
	dir, st := initRepo(t)
	write(t, dir, "a.txt", "one")
	for i, name := range []string{"100-T1", "200-T2", "300-T3"} {
		write(t, dir, "a.txt", strings.Repeat("x", i+1))
		if _, err := st.Snapshot(name, "T -> coder: step", "head: "); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name != "300-T3" || entries[0].TaskID != "T3" {
		t.Fatalf("List = %+v", entries)
	}
	if entries[0].Subject != "T -> coder: step" {
		t.Fatalf("subject = %q", entries[0].Subject)
	}
	// the user's own index stays untouched: everything is still untracked
	if status := gitRun(t, dir, "status", "--porcelain"); !strings.Contains(status, "?? a.txt") {
		t.Fatalf("user index touched:\n%s", status)
	}
	n, err := st.Prune(1)
	if err != nil || n != 2 {
		t.Fatalf("Prune = %d, %v", n, err)
	}
	if entries, _ = st.List(); len(entries) != 1 || entries[0].Name != "300-T3" {
		t.Fatalf("after prune = %+v", entries)
	}
}

func TestRollbackRestoresFilesNotHistory(t *testing.T) {
	dir, st := initRepo(t)
	write(t, dir, ".gitignore", "ignored.txt\n")
	write(t, dir, "tracked.txt", "v1")
	write(t, dir, "keep.txt", "orig")
	write(t, dir, "ignored.txt", "ig1")
	if err := os.MkdirAll(filepath.Join(dir, ".choragos"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".choragos/events.log", "audit-1")
	gitRun(t, dir, "add", "tracked.txt")
	gitRun(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	headBefore := st.Head()

	target, err := st.Snapshot("100-T1", "T1 -> coder: break things", "head: "+headBefore)
	if err != nil {
		t.Fatal(err)
	}
	// the worker wrecks the workspace
	write(t, dir, "tracked.txt", "v2")
	if err := os.Remove(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "extra.txt", "new junk")
	write(t, dir, "ignored.txt", "ig2")
	write(t, dir, ".choragos/events.log", "audit-1\naudit-2")

	pre, err := st.Snapshot("200-pre-rollback-T1", "pre-rollback -> 100-T1", "head: "+st.Head())
	if err != nil {
		t.Fatal(err)
	}
	changed, extra, err := st.Diff(target, pre)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 {
		t.Fatalf("changed = %v, want tracked/keep/extra", changed)
	}
	if len(extra) != 1 || extra[0] != "extra.txt" {
		t.Fatalf("extra = %v, want [extra.txt]", extra)
	}
	if err := st.Apply(target, extra); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "tracked.txt"); got != "v1" {
		t.Fatalf("tracked.txt = %q, want v1", got)
	}
	if got := read(t, dir, "keep.txt"); got != "orig" {
		t.Fatalf("keep.txt = %q, want orig", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "extra.txt")); !os.IsNotExist(err) {
		t.Fatal("extra.txt should be deleted")
	}
	if got := read(t, dir, "ignored.txt"); got != "ig2" {
		t.Fatalf("ignored.txt = %q; rollback must never touch ignored files", got)
	}
	if got := read(t, dir, ".choragos/events.log"); got != "audit-1\naudit-2" {
		t.Fatalf("events.log = %q; rollback must never touch choragos runtime state", got)
	}
	if st.Head() != headBefore {
		t.Fatalf("HEAD moved: %s -> %s", headBefore, st.Head())
	}
	if st.MetaHead(target) != headBefore {
		t.Fatalf("MetaHead = %q, want %q", st.MetaHead(target), headBefore)
	}
}
