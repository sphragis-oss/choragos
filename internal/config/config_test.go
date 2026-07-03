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

func TestLoadPricing(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "solo"
command = "sh"
start = true

[pricing."claude-sonnet-5"]
input = 3.0
output = 15.0
cache_read = 0.3
cache_creation = 3.75
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := c.Pricing["claude-sonnet-5"]
	if !ok || p.Input != 3.0 || p.Output != 15.0 || p.CacheRead != 0.3 || p.CacheCreation != 3.75 {
		t.Fatalf("pricing = %+v (ok=%v)", p, ok)
	}
	if len(c.Warnings) != 0 {
		t.Fatalf("pricing table should not warn: %v", c.Warnings)
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

func TestKeysDefaultsMatchHerdr(t *testing.T) {
	k := config.Default().Keys
	want := config.Keys{
		Prefix: "ctrl+b", SplitVertical: "v", SplitHorizontal: "-", ClosePane: "x",
		FocusLeft: "h", FocusDown: "j", FocusUp: "k", FocusRight: "l",
		CycleNext: "tab", CyclePrev: "shift+tab", Zoom: "z", ResizeMode: "r", ToggleSidebar: "b",
		Help: "?", RestartRole: "R", Broadcast: "a", TaskBoard: "t", Search: "/",
	}
	if k != want {
		t.Fatalf("default keys = %+v, want %+v", k, want)
	}
}

func TestKeysAndUIFromTOML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "solo"
command = "sh"
start = true

[keys]
prefix = "ctrl+a"
split_vertical = "prefix+s"
split_horizontal = "prefix+minus"

[ui]
auto_focus = false
sidebar = false
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Keys.Prefix != "ctrl+a" || c.Keys.SplitVertical != "s" || c.Keys.SplitHorizontal != "-" {
		t.Fatalf("keys override: %+v", c.Keys)
	}
	if c.Keys.Zoom != "z" || c.Keys.CyclePrev != "shift+tab" {
		t.Fatalf("omitted keys should default: %+v", c.Keys)
	}
	if c.UI.IsAutoFocus() || c.UI.SidebarStart() {
		t.Fatalf("ui overrides ignored: %+v", c.UI)
	}
}

func TestLoadWarnsOnUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "solo"
command = "sh"
start = true

[ui]
auto_focsu = false

[keyz]
prefix = "ctrl+a"
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 entries", c.Warnings)
	}
	joined := strings.Join(c.Warnings, "\n")
	for _, want := range []string{"auto_focsu", "keyz"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q: %v", want, c.Warnings)
		}
	}
	// a clean config produces no warnings
	clean, err := config.Load("")
	if err != nil || len(clean.Warnings) != 0 {
		t.Fatalf("clean load: warnings=%v err=%v", clean.Warnings, err)
	}
}

func TestUIDefaultsOn(t *testing.T) {
	c := config.Default()
	if !c.UI.IsAutoFocus() || !c.UI.SidebarStart() {
		t.Fatalf("ui should default on: %+v", c.UI)
	}
}

func TestExampleConfigsLoad(t *testing.T) {
	files, err := filepath.Glob("../../examples/*.toml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no example configs found: %v", err)
	}
	for _, f := range files {
		c, err := config.Load(f)
		if err != nil {
			t.Errorf("%s does not load: %v", f, err)
			continue
		}
		if len(c.Warnings) > 0 {
			t.Errorf("%s has unknown keys: %v", f, c.Warnings)
		}
	}
}

func FuzzKeysDefaulted(f *testing.F) {
	for _, seed := range []string{"", "v", "prefix+v", "PREFIX+MINUS", "ctrl+b", "minus", "shift+tab", "  x  ", "πλήκτρο"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k := config.Keys{Prefix: s, SplitVertical: s, Zoom: s}.Defaulted()
		if k.Prefix == "" || k.SplitVertical == "" || k.Zoom == "" {
			t.Fatalf("Defaulted left an empty binding for input %q: %+v", s, k)
		}
	})
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
