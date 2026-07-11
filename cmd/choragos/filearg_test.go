// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func TestAbsFileArg(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(good, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := absFileArg(good)
	if err != nil || !filepath.IsAbs(abs) {
		t.Fatalf("good file: abs=%q err=%v", abs, err)
	}
	if _, err := absFileArg(filepath.Join(dir, "missing.md")); err == nil {
		t.Fatal("missing file must error")
	}
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := absFileArg(empty); err == nil {
		t.Fatal("empty file must error")
	}
	if _, err := absFileArg(dir); err == nil {
		t.Fatal("directory must error")
	}
}

func runCmd(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestDelegateRequiresTaskOrBrief(t *testing.T) {
	err := runCmd(t, delegateCmd(), "--to", "coder")
	if err == nil || !strings.Contains(err.Error(), "--task or --brief") {
		t.Fatalf("err = %v", err)
	}
}

func TestDelegateRejectsBadBrief(t *testing.T) {
	err := runCmd(t, delegateCmd(), "--to", "coder", "--brief", filepath.Join(t.TempDir(), "nope.md"))
	if err == nil || !strings.Contains(err.Error(), "--brief") {
		t.Fatalf("err = %v", err)
	}
}

func TestWorkDoneRequiresTaskOrReport(t *testing.T) {
	err := runCmd(t, workDoneCmd())
	if err == nil || !strings.Contains(err.Error(), "--task or --report") {
		t.Fatalf("err = %v", err)
	}
}

func TestDelegateSendsAbsoluteBrief(t *testing.T) {
	sock := filepath.Join("/tmp", "chg-brief-cli.sock")
	t.Setenv(ipc.EnvSocket, sock)
	got := make(chan ipc.Command, 1)
	srv, err := ipc.Serve(sock, func(c ipc.Command) { got <- c })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("mission"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCmd(t, delegateCmd(), "--to", "coder", "--brief", brief); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	select {
	case c := <-got:
		if c.Cmd != "delegate" || c.Brief != brief || !filepath.IsAbs(c.Brief) {
			t.Fatalf("command = %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command not received")
	}
}
