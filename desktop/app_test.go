// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

// TestOnboardingBackend drives Templates/InitConfig/HasConfig/StartSession
// against the installed CLI; skipped where choragos is not on PATH.
func TestOnboardingBackend(t *testing.T) {
	cli, err := exec.LookPath("choragos")
	if err != nil {
		t.Skip("choragos not installed")
	}
	v, err := cliVersion(cli)
	if err != nil {
		t.Fatalf("cliVersion: %v", err)
	}
	// SHORT socket dir: macOS caps sun_path near 104 bytes
	short, err := os.MkdirTemp("/tmp", "cgob")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	t.Setenv("XDG_RUNTIME_DIR", short)

	a := newApp(v)
	dir := t.TempDir()
	if a.HasConfig(dir) {
		t.Fatal("HasConfig true on an empty dir")
	}
	if tmpls := a.Templates(); !slices.Contains(tmpls, "starter") {
		t.Fatalf("Templates() = %v, want it to include starter", tmpls)
	}
	note, err := a.InitConfig(dir, "starter", false)
	if err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	if note == "" || !a.HasConfig(dir) {
		t.Fatalf("InitConfig note=%q, HasConfig=%v", note, a.HasConfig(dir))
	}

	// replace the starter roles with harmless cat panes before serving
	team := `[sphragis]
enabled = false

[[roles]]
name = "orchestrator"
command = "sh"
args = ["-c", "exec cat"]
start = true
`
	if err := os.WriteFile(filepath.Join(dir, ".choragos.toml"), []byte(team), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.StartSession(dir); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_ = ipc.Send(sessionSocket(dir), ipc.Command{Cmd: "shutdown"})
		time.Sleep(500 * time.Millisecond)
	})
	if err := ipc.Send(sessionSocket(dir), ipc.Command{Cmd: "ping"}); err != nil {
		t.Fatalf("session not pingable after StartSession: %v", err)
	}
	// idempotent: a second StartSession sees the running session and returns
	if err := a.StartSession(dir); err != nil {
		t.Fatalf("StartSession on a running session: %v", err)
	}
}
