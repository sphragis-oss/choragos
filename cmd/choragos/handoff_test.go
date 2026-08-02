// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func TestHandoffCmd(t *testing.T) {
	sock := filepath.Join(shortRuntimeDir(t), "h.sock")
	t.Setenv(ipc.EnvSocket, sock)
	got := fakeDeck(t, sock)
	t.Chdir(t.TempDir())
	next := "next-team.toml"
	body := "[[roles]]\nname = \"orchestrator\"\ncommand = \"cat\"\nstart = true\n"
	if err := os.WriteFile(next, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := handoffCmd()
	cmd.SetArgs([]string{"--config", next})
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "handoff requested") {
		t.Errorf("handoff output = %q", out.String())
	}
	select {
	case c := <-got:
		if c.Cmd != "handoff" || !filepath.IsAbs(c.NextConfig) || filepath.Base(c.NextConfig) != next {
			t.Errorf("deck received %+v, want handoff with the absolute next config", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deck never received the handoff command")
	}
}

func TestHandoffCmdRefusals(t *testing.T) {
	shortRuntimeDir(t)
	t.Chdir(t.TempDir())
	cmd := handoffCmd()
	cmd.SetArgs([]string{"--config", "missing.toml"})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "next config") {
		t.Fatalf("an unloadable next config must refuse before sending: %v", err)
	}
	cmd = handoffCmd()
	cmd.SetArgs(nil)
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "is the deck running") {
		t.Fatalf("no session must fail with the hint: %v", err)
	}
}
