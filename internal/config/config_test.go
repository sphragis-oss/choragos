// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

func TestDefaultHasFiveRoles(t *testing.T) {
	c := config.Default()
	if len(c.Roles) != 5 {
		t.Fatalf("want 5 roles, got %d", len(c.Roles))
	}
	starts := 0
	for _, r := range c.Roles {
		if r.Start {
			starts++
			if r.Name != "orchestrator" {
				t.Errorf("start role is %q, want orchestrator", r.Name)
			}
		}
	}
	if starts != 1 {
		t.Errorf("want exactly 1 start role, got %d", starts)
	}
}

func TestLoadEmptyFallsBackToDefault(t *testing.T) {
	t.Chdir(t.TempDir()) // no .choragos.toml here
	c, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Roles) != 5 {
		t.Fatalf("want default 5 roles, got %d", len(c.Roles))
	}
}

func TestLoadTOML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "solo"
command = "sh"
args = ["-c", "true"]
model = "opus"
start = true
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Roles) != 1 || c.Roles[0].Name != "solo" || !c.Roles[0].Start {
		t.Fatalf("unexpected config: %+v", c)
	}
	if c.Roles[0].Model != "opus" {
		t.Errorf("model = %q, want opus", c.Roles[0].Model)
	}
}

func TestSphragisDefaults(t *testing.T) {
	c := config.Default()
	if !c.Sphragis.IsEnabled() || !c.Sphragis.IsFailClosed() {
		t.Fatal("sphragis should default to enabled + fail-closed")
	}
	if c.Sphragis.Addr != "127.0.0.1:8787" || c.Sphragis.Command != "sphragis" {
		t.Fatalf("sphragis defaults wrong: %+v", c.Sphragis)
	}
	if c.Sphragis.BaseURL() != "http://127.0.0.1:8787" {
		t.Errorf("BaseURL = %q", c.Sphragis.BaseURL())
	}
}

func TestSphragisDisableViaTOML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "solo"
command = "sh"
start = true

[sphragis]
enabled = false
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Sphragis.IsEnabled() {
		t.Fatal("sphragis should be disabled by config")
	}
	if c.Sphragis.Addr != "127.0.0.1:8787" {
		t.Errorf("omitted addr should still default, got %q", c.Sphragis.Addr)
	}
}

func TestCheckCommands(t *testing.T) {
	ok := config.Config{Roles: []config.Role{{Name: "a", Command: "sh"}}}
	if err := ok.CheckCommands(); err != nil {
		t.Errorf("sh should resolve on PATH: %v", err)
	}
	bad := config.Config{Roles: []config.Role{
		{Name: "coder", Command: "definitely-not-a-real-binary-xyz"},
	}}
	err := bad.CheckCommands()
	if err == nil {
		t.Fatal("expected error for a missing command")
	}
	if !strings.Contains(err.Error(), "coder") {
		t.Errorf("error should name the offending role: %v", err)
	}
}

func TestLoadNoRolesErrors(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "empty.toml")
	if err := os.WriteFile(f, []byte("# no roles\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(f); err == nil {
		t.Fatal("want error for config with no roles")
	}
}
