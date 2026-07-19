// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// nil records "never asked" so serve can auto-off when the binary is missing
	if c.Sphragis.Enabled != nil {
		t.Fatal("omitted enabled must stay nil, not be defaulted to true")
	}
}

func TestTimeoutValidation(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "ok"
command = "sh"
start = true
timeout = "45m"
timeout_action = "restart"

[[roles]]
name = "bad"
command = "sh"
timeout = "soon"
timeout_action = "explode"
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Roles[0].TimeoutDuration(); d != 45*time.Minute {
		t.Fatalf("timeout = %v, want 45m", d)
	}
	if c.Roles[0].TimeoutAction != "restart" {
		t.Fatalf("action = %q", c.Roles[0].TimeoutAction)
	}
	if c.Roles[1].Timeout != "" || c.Roles[1].TimeoutAction != "" {
		t.Fatalf("invalid values must reset: %+v", c.Roles[1])
	}
	if len(c.Warnings) != 2 {
		t.Fatalf("want 2 warnings, got %v", c.Warnings)
	}
}

func TestBaseURLEnvValidation(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "openai"
command = "sh"
start = true
base_url_env = ["OPENAI_BASE_URL", "OPENAI_API_BASE"]

[[roles]]
name = "bad"
command = "sh"
base_url_env = ["NOT=A_NAME"]
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Roles[0].BaseURLEnvNames(); len(got) != 2 || got[0] != "OPENAI_BASE_URL" {
		t.Fatalf("names = %v", got)
	}
	// invalid names reset to the default and warn
	if got := c.Roles[1].BaseURLEnvNames(); len(got) != 1 || got[0] != "ANTHROPIC_BASE_URL" {
		t.Fatalf("invalid base_url_env must fall back to the default, got %v", got)
	}
	if len(c.Warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", c.Warnings)
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
		Help: "?", RestartRole: "R", PauseRole: "p", Broadcast: "a", TaskBoard: "t", Search: "/", Reload: "C", Detach: "d",
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
mouse = false
on_gate = "notify-gate"
on_input = "notify-input"
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
	if c.UI.IsAutoFocus() || c.UI.SidebarStart() || c.UI.IsMouse() {
		t.Fatalf("ui overrides ignored: %+v", c.UI)
	}
	if c.UI.OnGate != "notify-gate" || c.UI.OnInput != "notify-input" {
		t.Fatalf("notification hooks not loaded: %+v", c.UI)
	}
	if c.Path != f {
		t.Fatalf("Path = %q, want %q", c.Path, f)
	}
	if len(c.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", c.Warnings)
	}
	if !(config.UI{}).IsMouse() {
		t.Fatal("mouse must default to enabled")
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

func TestLoadValidatesViewer(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "solo"
command = "sh"
start = true

[ui]
viewer = "emacsclient"
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "emacsclient") {
		t.Fatalf("warnings = %v, want one about viewer", c.Warnings)
	}
	if c.UI.IsEditorViewer() {
		t.Fatal("invalid viewer must fall back to the pager")
	}
	if !(config.UI{Viewer: "editor"}).IsEditorViewer() || (config.UI{Viewer: "pager"}).IsEditorViewer() {
		t.Fatal("IsEditorViewer must reflect the setting")
	}
}

func TestLoadValidatesTheme(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "solo"
command = "sh"
start = true

[ui.theme]
accent = "#ff00ff"
waiting = "256"
dim = "chartreuse"
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
	for _, want := range []string{`waiting: invalid color "256"`, `dim: invalid color "chartreuse"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q: %v", want, c.Warnings)
		}
	}
	// valid values survive, invalid ones fall back to empty (deck default)
	if c.UI.Theme.Accent != "#ff00ff" || c.UI.Theme.Waiting != "" || c.UI.Theme.Dim != "" {
		t.Fatalf("theme = %+v", c.UI.Theme)
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

func TestRestartOptions(t *testing.T) {
	r := config.Role{}
	if r.RestartOnFailure() || r.RestartCap() != 3 {
		t.Fatalf("defaults: on-failure=%v cap=%d, want false/3", r.RestartOnFailure(), r.RestartCap())
	}
	r = config.Role{Restart: "on-failure", RestartRetries: 5}
	if !r.RestartOnFailure() || r.RestartCap() != 5 {
		t.Fatalf("configured: on-failure=%v cap=%d, want true/5", r.RestartOnFailure(), r.RestartCap())
	}
}

func TestLoadWarnsUnknownRestartMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	body := "[[roles]]\nname = \"a\"\ncommand = \"cat\"\nrestart = \"always\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range c.Warnings {
		if strings.Contains(w, "unknown restart mode") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected restart-mode warning, got %v", c.Warnings)
	}
}

func TestLoadJudgeValidation(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "orchestrator"
command = "sh"
start = true

[[roles]]
name = "coder"
command = "sh"
judge = "coder"

[[roles]]
name = "tester"
command = "sh"
judge = "ghost"

[[roles]]
name = "writer"
command = "sh"
judge = "orchestrator"
judge_pass = 15
judge_rounds = -2
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Roles[1].Judge; got != "" {
		t.Errorf("self-judge kept: %q", got)
	}
	if got := c.Roles[2].Judge; got != "" {
		t.Errorf("unknown judge kept: %q", got)
	}
	w := c.Roles[3]
	if w.Judge != "orchestrator" {
		t.Errorf("valid judge dropped: %q", w.Judge)
	}
	if w.JudgePassScore() != 7 || w.JudgeCap() != 3 {
		t.Errorf("out-of-range judge_pass/judge_rounds not defaulted: pass=%d cap=%d", w.JudgePassScore(), w.JudgeCap())
	}
	if len(c.Warnings) != 4 {
		t.Errorf("want 4 warnings (self, unknown, pass range, negative rounds), got %d: %v", len(c.Warnings), c.Warnings)
	}
}

func TestJudgeDefaults(t *testing.T) {
	r := config.Role{JudgePass: 9, JudgeRounds: 5}
	if r.JudgePassScore() != 9 || r.JudgeCap() != 5 {
		t.Fatalf("explicit values not honored: pass=%d cap=%d", r.JudgePassScore(), r.JudgeCap())
	}
	z := config.Role{}
	if z.JudgePassScore() != 7 || z.JudgeCap() != 3 {
		t.Fatalf("defaults wrong: pass=%d cap=%d", z.JudgePassScore(), z.JudgeCap())
	}
}
