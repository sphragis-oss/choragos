// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func TestMain(m *testing.M) {
	// detachServe re-execs this test binary as "serve --headless"; exit before the suite recurses
	if os.Getenv("CHORAGOS_DETACH_TEST_CHILD") == "1" {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestServeDetachStartsChildAndTightensLogs(t *testing.T) {
	shortRuntimeDir(t)
	t.Chdir(t.TempDir())
	t.Setenv("CHORAGOS_DETACH_TEST_CHILD", "1")
	cfg := "[[roles]]\nname = \"orchestrator\"\ncommand = \"cat\"\nstart = true\n\n[sphragis]\nenabled = false\n"
	if err := os.WriteFile(".choragos.toml", []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := serveCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--detach"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("serve --detach: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "session started") {
		t.Fatalf("missing start message:\n%s", out.String())
	}
	di, err := os.Stat(filepath.Join(".choragos", "logs"))
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("logs dir mode = %o, want 700", got)
	}
	fi, err := os.Stat(filepath.Join(".choragos", "logs", "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("server.log mode = %o, want 600", got)
	}
}

func TestServeResumeRefusesWithoutSnapshot(t *testing.T) {
	shortRuntimeDir(t)
	t.Chdir(t.TempDir())
	cfg := "[[roles]]\nname = \"orchestrator\"\ncommand = \"cat\"\nstart = true\n\n[sphragis]\nenabled = false\n"
	if err := os.WriteFile(".choragos.toml", []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := serveCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--resume"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no session snapshot") {
		t.Fatalf("serve --resume without a snapshot must refuse: %v", err)
	}
}

func TestServeHeadlessResumeRunsAndShutsDown(t *testing.T) {
	shortRuntimeDir(t)
	t.Chdir(t.TempDir())
	cfg := "[[roles]]\nname = \"orchestrator\"\ncommand = \"cat\"\nstart = true\n\n[sphragis]\nenabled = false\n"
	if err := os.WriteFile(".choragos.toml", []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(".choragos", 0o755); err != nil {
		t.Fatal(err)
	}
	snap := `{"version": 1, "config_path": ".choragos.toml", "task_seq": 2, "roster": [{"name": "orchestrator"}]}`
	if err := os.WriteFile(filepath.Join(".choragos", "session.json"), []byte(snap), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := serveCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--headless", "--resume"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "shutdown"}); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("headless resume: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("headless server did not shut down")
	}
	data, err := os.ReadFile(filepath.Join(".choragos", "logs", "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "session resumed") {
		t.Fatalf("events.log missing the resume marker:\n%s", data)
	}
}

func TestServeDetachForwardsResume(t *testing.T) {
	shortRuntimeDir(t)
	t.Chdir(t.TempDir())
	t.Setenv("CHORAGOS_DETACH_TEST_CHILD", "1")
	cfg := "[[roles]]\nname = \"orchestrator\"\ncommand = \"cat\"\nstart = true\n\n[sphragis]\nenabled = false\n"
	if err := os.WriteFile(".choragos.toml", []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := `{"version": 1, "config_path": ".choragos.toml", "roster": [{"name": "orchestrator"}]}`
	if err := os.MkdirAll(".choragos", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".choragos", "session.json"), []byte(snap), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := serveCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--detach", "--resume"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("serve --detach --resume: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "session started") {
		t.Fatalf("missing start message:\n%s", out.String())
	}
}
