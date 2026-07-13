// SPDX-License-Identifier: Apache-2.0

// Package checkpoint snapshots the workspace around delegations with git plumbing.
package checkpoint

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RefPrefix is the ref namespace holding checkpoint commits.
const RefPrefix = "refs/choragos/checkpoints/"

// excludeDir keeps choragos's own runtime state (logs, task files) out of snapshots and rollbacks.
const excludeDir = ":(exclude).choragos"

// Store runs git plumbing for one working directory; call Active before anything else.
type Store struct {
	dir  string
	root string // repo toplevel, resolved by Active
}

// Entry is one checkpoint: a parentless commit named <unix-epoch>-<task-id>.
type Entry struct {
	Name    string
	Ref     string
	TaskID  string
	At      time.Time
	Subject string
}

func New(dir string) *Store { return &Store{dir: dir} }

// Active reports whether checkpoints can work here; reason explains when not.
func (s *Store) Active() (bool, string) {
	if _, err := exec.LookPath("git"); err != nil {
		return false, "git not found in PATH"
	}
	out, err := s.git("rev-parse", "--show-toplevel")
	if err != nil {
		return false, "not a git repository"
	}
	s.root = strings.TrimSpace(out)
	return true, ""
}

// git runs one git command in the store's directory and returns its stdout.
func (s *Store) git(args ...string) (string, error) {
	return s.gitEnv(nil, args...)
}

// gitEnv is git with extra environment (the temp-index override).
func (s *Store) gitEnv(env []string, args ...string) (string, error) {
	dir := s.root
	if dir == "" {
		dir = s.dir
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if env != nil {
		cmd.Env = env
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// withIndex hands fn an env whose GIT_INDEX_FILE is a throwaway, so the user's index is never touched.
func (s *Store) withIndex(fn func(env []string) error) error {
	tmp, err := os.MkdirTemp("", "choragos-ckpt")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))
	return fn(env)
}

// Snapshot records tracked+untracked files (gitignore respected) as a parentless commit under RefPrefix/name.
func (s *Store) Snapshot(name, subject, body string) (string, error) {
	ref := RefPrefix + name
	err := s.withIndex(func(env []string) error {
		if _, err := s.gitEnv(env, "add", "-A", "--", ".", excludeDir); err != nil {
			return err
		}
		tree, err := s.gitEnv(env, "write-tree")
		if err != nil {
			return err
		}
		commit, err := s.git("-c", "user.name=choragos", "-c", "user.email=choragos@localhost",
			"commit-tree", strings.TrimSpace(tree), "-m", subject, "-m", body)
		if err != nil {
			return err
		}
		_, err = s.git("update-ref", ref, strings.TrimSpace(commit))
		return err
	})
	if err != nil {
		return "", err
	}
	return ref, nil
}

// Head returns the current HEAD sha, or "" before the first commit.
func (s *Store) Head() string {
	out, err := s.git("rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// MetaHead returns the "head:" sha recorded in a checkpoint's message, if any.
func (s *Store) MetaHead(ref string) string {
	out, err := s.git("log", "-1", "--format=%b", ref)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "head: "); ok {
			return v
		}
	}
	return ""
}

// List returns the checkpoints, newest first (epoch-prefixed names sort lexically).
func (s *Store) List() ([]Entry, error) {
	out, err := s.git("for-each-ref", "--sort=-refname", "--format=%(refname)%09%(subject)", RefPrefix)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		ref, subject, _ := strings.Cut(line, "\t")
		name := strings.TrimPrefix(ref, RefPrefix)
		epoch, task, ok := strings.Cut(name, "-")
		sec, err := strconv.ParseInt(epoch, 10, 64)
		if !ok || err != nil {
			continue // foreign ref in our namespace
		}
		entries = append(entries, Entry{Name: name, Ref: ref, TaskID: task, At: time.Unix(sec, 0), Subject: subject})
	}
	return entries, nil
}

// Prune deletes all but the newest keep checkpoints and returns how many were removed.
func (s *Store) Prune(keep int) (int, error) {
	entries, err := s.List()
	if err != nil {
		return 0, err
	}
	removed := 0
	for i := keep; i < len(entries); i++ {
		if _, err := s.git("update-ref", "-d", entries[i].Ref); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// Diff compares a pre-rollback snapshot against the target: all differing paths, and extras to delete.
func (s *Store) Diff(target, pre string) (changed, extra []string, err error) {
	if changed, err = s.diffZ(pre, target); err != nil {
		return nil, nil, err
	}
	// added going target->pre = present now, absent in the checkpoint
	extra, err = s.diffZ(target, pre, "--diff-filter=A")
	return changed, extra, err
}

func (s *Store) diffZ(from, to string, opts ...string) ([]string, error) {
	args := append([]string{"diff", "--name-only", "-z"}, opts...)
	out, err := s.git(append(args, from, to)...)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// Apply makes the worktree match the target tree: restore every file, delete extras, ignore ignored.
func (s *Store) Apply(target string, extra []string) error {
	err := s.withIndex(func(env []string) error {
		if _, err := s.gitEnv(env, "read-tree", target); err != nil {
			return err
		}
		_, err := s.gitEnv(env, "checkout-index", "-f", "-a")
		return err
	})
	if err != nil {
		return err
	}
	for _, p := range extra {
		if err := os.Remove(filepath.Join(s.root, p)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
