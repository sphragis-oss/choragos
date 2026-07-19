// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/checkpoint"
	"github.com/sphragis-oss/choragos/internal/ipc"
)

// shortRuntimeDir pins session paths under /tmp; macOS caps sun_path near 104 bytes.
func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "chox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv(ipc.EnvSocket, "")
	return dir
}

func runCLI(t *testing.T, cmd *cobra.Command, args []string) (string, error) {
	t.Helper()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.RunE(cmd, args)
	return out.String(), err
}

func fakeDeck(t *testing.T, sock string) chan ipc.Command {
	t.Helper()
	got := make(chan ipc.Command, 8)
	srv, err := ipc.Serve(sock, func(c ipc.Command) { got <- c })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return got
}

func TestVersionCmd(t *testing.T) {
	out, err := runCLI(t, versionCmd(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "choragos "+version) {
		t.Errorf("version output = %q", out)
	}
}

func TestReloadCmd(t *testing.T) {
	sock := filepath.Join(shortRuntimeDir(t), "r.sock")
	t.Setenv(ipc.EnvSocket, sock)
	got := fakeDeck(t, sock)

	out, err := runCLI(t, reloadCmd(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reload requested") {
		t.Errorf("reload output = %q", out)
	}
	select {
	case c := <-got:
		if c.Cmd != "reload" {
			t.Errorf("deck received %+v, want reload", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deck never received the reload command")
	}

	t.Setenv(ipc.EnvSocket, filepath.Join(shortRuntimeDir(t), "absent.sock"))
	if _, err := runCLI(t, reloadCmd(), nil); err == nil || !strings.Contains(err.Error(), "is the deck running") {
		t.Errorf("reload without a deck should hint at the deck, got: %v", err)
	}
}

func TestWorkDoneCmd(t *testing.T) {
	if _, err := runCLI(t, workDoneCmd(), nil); err == nil || !strings.Contains(err.Error(), "--task or --report") {
		t.Errorf("work-done without flags should demand --task or --report, got: %v", err)
	}

	sock := filepath.Join(shortRuntimeDir(t), "w.sock")
	t.Setenv(ipc.EnvSocket, sock)
	got := fakeDeck(t, sock)

	cmd := workDoneCmd()
	for flag, val := range map[string]string{"task": "did the thing", "done": "true", "id": "t1"} {
		if err := cmd.Flags().Set(flag, val); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runCLI(t, cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reported to orchestrator") {
		t.Errorf("work-done output = %q", out)
	}
	select {
	case c := <-got:
		if c.Cmd != "work-done" || c.Task != "did the thing" || !c.Done || c.ID != "t1" {
			t.Errorf("deck received %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deck never received work-done")
	}

	// a --report path that does not exist must be rejected before any send
	bad := workDoneCmd()
	if err := bad.Flags().Set("report", filepath.Join(t.TempDir(), "missing.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, bad, nil); err == nil || !strings.Contains(err.Error(), "--report") {
		t.Errorf("missing report file should fail, got: %v", err)
	}
}

func TestRosterAddCmd(t *testing.T) {
	if _, err := runCLI(t, rosterAddCmd(), nil); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Errorf("roster add without flags should demand --name, got: %v", err)
	}
	noCmd := rosterAddCmd()
	if err := noCmd.Flags().Set("name", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, noCmd, nil); err == nil || !strings.Contains(err.Error(), "--command") {
		t.Errorf("roster add without --command should fail, got: %v", err)
	}

	sock := filepath.Join(shortRuntimeDir(t), "r.sock")
	t.Setenv(ipc.EnvSocket, sock)
	got := fakeDeck(t, sock)

	cmd := rosterAddCmd()
	for flag, val := range map[string]string{
		"name": "tester", "command": "cat", "model": "sonnet", "prompt-template": "brief",
	} {
		if err := cmd.Flags().Set(flag, val); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []string{"-u", "-b"} {
		if err := cmd.Flags().Set("arg", a); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runCLI(t, cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "proposed role tester") {
		t.Errorf("roster add output = %q", out)
	}
	select {
	case c := <-got:
		if c.Cmd != "roster-add" || c.RoleName != "tester" || c.RoleCommand != "cat" ||
			c.RoleModel != "sonnet" || c.RolePrompt != "brief" ||
			len(c.RoleArgs) != 2 || c.RoleArgs[0] != "-u" || c.RoleArgs[1] != "-b" {
			t.Errorf("deck received %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deck never received roster-add")
	}

	// no deck on the socket: the send error surfaces with the running hint
	t.Setenv(ipc.EnvSocket, filepath.Join(shortRuntimeDir(t), "absent.sock"))
	dead := rosterAddCmd()
	for flag, val := range map[string]string{"name": "tester", "command": "cat"} {
		if err := dead.Flags().Set(flag, val); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runCLI(t, dead, nil); err == nil || !strings.Contains(err.Error(), "is the deck running") {
		t.Errorf("dead socket should fail with the hint, got: %v", err)
	}
}

func TestSessionsLifecycle(t *testing.T) {
	shortRuntimeDir(t)
	t.Chdir(t.TempDir())

	out, err := runCLI(t, lsCmd(), nil)
	if err != nil || !strings.Contains(out, "no running sessions") {
		t.Fatalf("ls with no sessions: %q err %v", out, err)
	}

	sock := ipc.SocketPath()
	got := fakeDeck(t, sock)
	ipc.WriteMeta(sock)

	out, err = runCLI(t, lsCmd(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pid") || strings.Contains(out, "no running sessions") {
		t.Errorf("ls with a live session = %q", out)
	}
	<-got // the liveness ping

	out, err = runCLI(t, killCmd(), nil)
	if err != nil || !strings.Contains(out, "session stopped") {
		t.Fatalf("kill live session: %q err %v", out, err)
	}
	if c := <-got; c.Cmd != "shutdown" {
		t.Errorf("deck received %+v, want shutdown", c)
	}
}

func TestSessionsStaleAndAbsent(t *testing.T) {
	shortRuntimeDir(t)
	t.Chdir(t.TempDir())

	// a meta whose socket is dead is pruned, not listed
	ipc.WriteMeta(filepath.Join("/tmp", "chg-dead.sock"))
	out, err := runCLI(t, lsCmd(), nil)
	if err != nil || !strings.Contains(out, "no running sessions") {
		t.Fatalf("ls with a stale meta: %q err %v", out, err)
	}
	if _, err := os.Stat(ipc.MetaPath()); !os.IsNotExist(err) {
		t.Error("stale meta sidecar should have been pruned")
	}

	if _, err := runCLI(t, killCmd(), nil); err == nil || !strings.Contains(err.Error(), "no session running") {
		t.Errorf("kill without a session, got: %v", err)
	}
	all := killCmd()
	if err := all.Flags().Set("all", "true"); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, all, nil)
	if err != nil || !strings.Contains(out, "no running sessions") {
		t.Fatalf("kill --all with none: %q err %v", out, err)
	}
}

func TestReportCmdMissingLog(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := runCLI(t, reportCmd(), nil); err == nil {
		t.Error("report without an events.log should fail")
	}
	if _, err := runCLI(t, reportCmd(), []string{filepath.Join(t.TempDir(), "nope.log")}); err == nil {
		t.Error("report with a missing path should fail")
	}
}

func TestAttachCmdNoSession(t *testing.T) {
	shortRuntimeDir(t)
	t.Chdir(t.TempDir())
	_, err := runCLI(t, attachCmd(), nil)
	if err == nil || !strings.Contains(err.Error(), "no session for this directory") {
		t.Errorf("attach without a session, got: %v", err)
	}
}

func TestCheckpointsCmdOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := runCLI(t, checkpointsCmd(), nil); err == nil || !strings.Contains(err.Error(), "checkpoints unavailable") {
		t.Errorf("checkpoints outside a git repo, got: %v", err)
	}
}

func TestHumanizeAge(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "30s",
		3 * time.Minute:  "3m",
		2 * time.Hour:    "2h",
		49 * time.Hour:   "2d",
	}
	for d, want := range cases {
		if got := humanizeAge(d); got != want {
			t.Errorf("humanizeAge(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFindCheckpoint(t *testing.T) {
	entries := []checkpoint.Entry{
		{TaskID: "t2", Name: "n2", Ref: "refs/choragos/checkpoints/n2"},
		{TaskID: "t1", Name: "n1", Ref: "refs/choragos/checkpoints/n1"},
	}
	for _, arg := range []string{"t1", "n1", "refs/choragos/checkpoints/n1"} {
		e, ok := findCheckpoint(entries, arg)
		if !ok || e.TaskID != "t1" {
			t.Errorf("findCheckpoint(%q) = %+v ok=%v", arg, e, ok)
		}
	}
	if _, ok := findCheckpoint(entries, "nope"); ok {
		t.Error("findCheckpoint should miss an unknown id")
	}
}

func TestConfirm(t *testing.T) {
	for input, want := range map[string]bool{"y\n": true, "yes\n": true, "n\n": false, "\n": false, "": false} {
		cmd := &cobra.Command{}
		cmd.SetOut(&strings.Builder{})
		cmd.SetIn(strings.NewReader(input))
		if got := confirm(cmd); got != want {
			t.Errorf("confirm(%q) = %v, want %v", input, got, want)
		}
	}
}
