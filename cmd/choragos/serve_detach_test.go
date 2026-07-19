// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
