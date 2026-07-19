// SPDX-License-Identifier: Apache-2.0

// Package config loads the orchestration role set, overridable via TOML.
package config

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DefaultFile is the config looked up in the working directory when no path is given.
const DefaultFile = ".choragos.toml"

// Role is one agent seat in the deck.
type Role struct {
	Name    string   `toml:"name"`
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
	Model   string   `toml:"model"`
	// flag that passes model to the command (default "--model"); "" = do not pass
	ModelFlag *string `toml:"model_flag"`
	Prompt    string  `toml:"prompt_template"`
	Start     bool    `toml:"start"`
	// extra markers appended to the built-in status heuristics for this agent's TUI
	InputPrompts  []string `toml:"input_prompts"`
	ChromeMarkers []string `toml:"chrome_markers"`
	// env isolation: when env_allow is set the role gets only baseline vars plus these
	// (exact names or "PREFIX_*" patterns); env_deny strips matches in either mode
	EnvAllow []string `toml:"env_allow"`
	EnvDeny  []string `toml:"env_deny"`
	// gateway wiring: env var name(s) that receive this role's agent URL (default ANTHROPIC_BASE_URL)
	BaseURLEnv []string `toml:"base_url_env"`
	// fresh respawns the role before every delegation, so each task starts with clean agent context
	Fresh bool `toml:"fresh"`
	// supervision: restart "on-failure" respawns the role on non-zero exit, capped by restart_retries
	Restart        string `toml:"restart"`
	RestartRetries int    `toml:"restart_retries"`
	// human gate: delegations to this role pause in the deck until the user approves
	Approve bool `toml:"approve"`
	// machine gate: delegations to this role are scored by the judge role and retried until judge_pass or judge_rounds
	Judge       string `toml:"judge"`
	JudgePass   int    `toml:"judge_pass"`
	JudgeRounds int    `toml:"judge_rounds"`
	// wall-clock limit per delegation ("45m"); empty = disabled. Action: "notify" (default) or "restart"
	Timeout       string `toml:"timeout"`
	TimeoutAction string `toml:"timeout_action"`
	// session cost cap in USD ("5.00"); empty = disabled. Action: "notify" (default) or "pause"
	Budget       string `toml:"budget"`
	BudgetAction string `toml:"budget_action"`
}

// BudgetUSD returns the role's session cost cap; zero means disabled. Load validated the format.
func (r Role) BudgetUSD() float64 {
	v, _ := strconv.ParseFloat(r.Budget, 64)
	return v
}

// TimeoutDuration returns the per-delegation wall-clock limit; zero means disabled. Load validated the format.
func (r Role) TimeoutDuration() time.Duration {
	d, _ := time.ParseDuration(r.Timeout)
	return d
}

// RestartOnFailure reports whether the role respawns when its process exits non-zero.
func (r Role) RestartOnFailure() bool { return r.Restart == "on-failure" }

// ModelFlagName returns the flag that passes model (default "--model"); empty means do not pass it.
func (r Role) ModelFlagName() string {
	if r.ModelFlag == nil {
		return "--model"
	}
	return *r.ModelFlag
}

// BaseURLEnvNames returns the env vars that receive this role's agent URL.
func (r Role) BaseURLEnvNames() []string {
	if len(r.BaseURLEnv) > 0 {
		return r.BaseURLEnv
	}
	return []string{"ANTHROPIC_BASE_URL"}
}

// JudgeCap returns the max builder->judge rounds (default 3), so a failing loop cannot run forever.
func (r Role) JudgeCap() int {
	if r.JudgeRounds > 0 {
		return r.JudgeRounds
	}
	return 3
}

// JudgePassScore returns the minimum passing score out of 10 (default 7).
func (r Role) JudgePassScore() int {
	if r.JudgePass > 0 {
		return r.JudgePass
	}
	return 7
}

// RestartCap returns the auto-restart limit (default 3), so a broken command cannot crash-loop.
func (r Role) RestartCap() int {
	if r.RestartRetries > 0 {
		return r.RestartRetries
	}
	return 3
}

// Config is the full orchestration.
type Config struct {
	Roles       []Role      `toml:"roles"`
	Sphragis    Sphragis    `toml:"sphragis"`
	Keys        Keys        `toml:"keys"`
	UI          UI          `toml:"ui"`
	Checkpoints Checkpoints `toml:"checkpoints"`
	Roster      Roster      `toml:"roster"`
	// Pricing maps a model-name prefix to USD per million tokens, for the cost display.
	Pricing map[string]Price `toml:"pricing"`
	// Warnings collects non-fatal load diagnostics (unknown keys, likely typos).
	Warnings []string `toml:"-"`
	// Path is the file this config was loaded from; empty for the built-in default (not reloadable).
	Path string `toml:"-"`
}

// Price is a model's USD cost per million tokens, by direction.
type Price struct {
	Input         float64 `toml:"input"`
	Output        float64 `toml:"output"`
	CacheRead     float64 `toml:"cache_read"`
	CacheCreation float64 `toml:"cache_creation"`
}

// Keys maps the prefix chord and the prefix-mode action keys (bubbletea key names).
type Keys struct {
	Prefix          string `toml:"prefix"`
	SplitVertical   string `toml:"split_vertical"`
	SplitHorizontal string `toml:"split_horizontal"`
	ClosePane       string `toml:"close_pane"`
	FocusLeft       string `toml:"focus_pane_left"`
	FocusDown       string `toml:"focus_pane_down"`
	FocusUp         string `toml:"focus_pane_up"`
	FocusRight      string `toml:"focus_pane_right"`
	CycleNext       string `toml:"cycle_pane_next"`
	CyclePrev       string `toml:"cycle_pane_previous"`
	Zoom            string `toml:"zoom"`
	ResizeMode      string `toml:"resize_mode"`
	ToggleSidebar   string `toml:"toggle_sidebar"`
	Help            string `toml:"help"`
	RestartRole     string `toml:"restart_role"`
	PauseRole       string `toml:"pause_role"`
	Broadcast       string `toml:"broadcast"`
	TaskBoard       string `toml:"task_board"`
	Search          string `toml:"search"`
	Reload          string `toml:"reload"`
	Detach          string `toml:"detach"`
}

// Defaulted fills empty bindings with the herdr default keymap and normalizes herdr syntax.
func (k Keys) Defaulted() Keys {
	set := func(p *string, def string) {
		v := strings.TrimSpace(*p)
		if strings.HasPrefix(strings.ToLower(v), "prefix+") {
			v = v[len("prefix+"):]
		}
		if len([]rune(v)) > 1 {
			v = strings.ToLower(v) // named keys (tab, ctrl+b); single runes keep case
		}
		if v == "minus" {
			v = "-"
		}
		if v == "" {
			v = def
		}
		*p = v
	}
	set(&k.Prefix, "ctrl+b")
	set(&k.SplitVertical, "v")
	set(&k.SplitHorizontal, "-")
	set(&k.ClosePane, "x")
	set(&k.FocusLeft, "h")
	set(&k.FocusDown, "j")
	set(&k.FocusUp, "k")
	set(&k.FocusRight, "l")
	set(&k.CycleNext, "tab")
	set(&k.CyclePrev, "shift+tab")
	set(&k.Zoom, "z")
	set(&k.ResizeMode, "r")
	set(&k.ToggleSidebar, "b")
	set(&k.Help, "?")
	set(&k.RestartRole, "R")
	set(&k.PauseRole, "p")
	set(&k.Broadcast, "a")
	set(&k.TaskBoard, "t")
	set(&k.Search, "/")
	set(&k.Reload, "C")
	set(&k.Detach, "d")
	return k
}

// UI tunes deck behavior; pointers so omitted = default true.
type UI struct {
	AutoFocus *bool `toml:"auto_focus"`
	Sidebar   *bool `toml:"sidebar"`
	Bell      *bool `toml:"bell"`
	Mouse     *bool `toml:"mouse"`
	// notification hooks, run via sh -c when the deck wants a human; empty = bell only
	OnGate    string `toml:"on_gate"`
	OnInput   string `toml:"on_input"`
	OnTimeout string `toml:"on_timeout"`
	OnBudget  string `toml:"on_budget"`
	Viewer    string `toml:"viewer"` // how v opens briefs/reports: "pager" (default) or "editor"
	Theme     Theme  `toml:"theme"`
}

// Theme overrides the deck's status colors; values are ANSI 0-255 or #rrggbb hex.
type Theme struct {
	Accent  string `toml:"accent"`
	Working string `toml:"working"`
	Waiting string `toml:"waiting"`
	Scroll  string `toml:"scroll"`
	Idle    string `toml:"idle"`
	Dim     string `toml:"dim"`
}

// IsAutoFocus reports whether delegations and input prompts steal focus (default true).
func (u UI) IsAutoFocus() bool { return u.AutoFocus == nil || *u.AutoFocus }

// SidebarStart reports whether the status-card sidebar starts visible (default true).
func (u UI) SidebarStart() bool { return u.Sidebar == nil || *u.Sidebar }

// IsBell reports whether a waiting-for-input transition rings the terminal bell (default true).
func (u UI) IsBell() bool { return u.Bell == nil || *u.Bell }

// IsMouse reports whether the deck captures the mouse (default true); off restores terminal-native selection.
func (u UI) IsMouse() bool { return u.Mouse == nil || *u.Mouse }

// IsEditorViewer reports whether v opens files in $VISUAL/$EDITOR instead of the in-app pager.
func (u UI) IsEditorViewer() bool { return u.Viewer == "editor" }

// Checkpoints controls per-delegation workspace snapshots (git repositories only).
type Checkpoints struct {
	Enabled *bool `toml:"enabled"`
	Keep    int   `toml:"keep"`
}

// IsEnabled reports whether delegations snapshot the workspace (default true).
func (c Checkpoints) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Roster controls orchestrator-driven roster proposals.
type Roster struct {
	Propose *bool `toml:"propose"`
	Approve *bool `toml:"approve"`
}

// CanPropose reports whether the orchestrator may propose roster changes (default true).
func (r Roster) CanPropose() bool { return r.Propose == nil || *r.Propose }

// NeedsApprove reports whether roster proposals pause at a human gate (default true).
func (r Roster) NeedsApprove() bool { return r.Approve == nil || *r.Approve }

// KeepCount returns how many checkpoints to retain (default 20).
func (c Checkpoints) KeepCount() int {
	if c.Keep > 0 {
		return c.Keep
	}
	return 20
}

// Sphragis controls routing agent traffic through the gateway; Enabled/FailClosed are pointers so omitted = on.
type Sphragis struct {
	Enabled    *bool  `toml:"enabled"`
	Addr       string `toml:"addr"`
	Command    string `toml:"command"`
	FailClosed *bool  `toml:"fail_closed"`
}

// CheckCommands verifies every role's command resolves on PATH so the deck fails fast (aliases will not resolve).
func (c Config) CheckCommands() error {
	var missing []string
	for _, r := range c.Roles {
		if _, err := exec.LookPath(r.Command); err != nil {
			missing = append(missing, fmt.Sprintf("%s (%q)", r.Name, r.Command))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("role command(s) not found in PATH: %s (if it is a shell alias, set the role's command to the real binary)",
			strings.Join(missing, ", "))
	}
	return nil
}

// IsEnabled reports whether traffic should route through the gateway (default true).
func (s Sphragis) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// IsFailClosed reports whether delegation is refused when the gateway is down (default true).
func (s Sphragis) IsFailClosed() bool { return s.FailClosed == nil || *s.FailClosed }

// BaseURL is the URL agents point ANTHROPIC_BASE_URL at.
func (s Sphragis) BaseURL() string { return "http://" + s.Addr }

func (s *Sphragis) applyDefaults() {
	// Enabled stays nil when omitted: default-on, but records that the user never asked (see sphragis.AutoOff)
	if s.FailClosed == nil {
		v := true
		s.FailClosed = &v
	}
	if s.Addr == "" {
		s.Addr = "127.0.0.1:8787"
	}
	if s.Command == "" {
		s.Command = "sphragis"
	}
}

// Default returns the built-in 5-role team (crew mapping), overridable via TOML.
func Default() Config {
	c := Config{Roles: []Role{
		{
			Name: "orchestrator", Command: "claude", Model: "opus", Start: true,
			Prompt: "You coordinate the team. Plan and delegate only; never implement, review, or audit yourself.",
		},
		{
			Name: "coder", Command: "claude", Model: "opus",
			Prompt: "Implement the requested change. Run the project's tests before reporting done.",
		},
		{
			Name: "reviewer", Command: "agy", Model: "Gemini 3.1 Pro (High)",
			Prompt: "Review the change for correctness and edge cases. Report findings only; do not modify code.",
		},
		{
			Name: "auditor", Command: "claude", Model: "sonnet",
			Prompt: "Audit the change for security issues and unsafe patterns. Report findings only.",
		},
		{
			Name: "release", Command: "claude", Model: "haiku",
			Prompt: "Run the release flow after the user validates end to end. Never modify source code.",
		},
	}}
	c.Sphragis.applyDefaults()
	c.Keys = c.Keys.Defaulted()
	return c
}

// Load reads the config at path, or DefaultFile in cwd, falling back to Default when absent.
// validColor accepts what lipgloss renders: an ANSI index 0-255 or #rrggbb hex.
func validColor(v string) bool {
	if len(v) == 7 && v[0] == '#' {
		_, err := strconv.ParseUint(v[1:], 16, 32)
		return err == nil
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= 0 && n <= 255
}

func Load(path string) (Config, error) {
	if path == "" {
		if _, err := os.Stat(DefaultFile); err != nil {
			return Default(), nil
		}
		path = DefaultFile
	}
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return Config{}, fmt.Errorf("load config %s: %w", path, err)
	}
	var unknown []string
	for _, k := range md.Undecoded() {
		ks := k.String()
		child := false
		for _, u := range unknown {
			if strings.HasPrefix(ks, u+".") {
				child = true // parent table already reported
				break
			}
		}
		if !child {
			unknown = append(unknown, ks)
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: unknown key %q (typo?)", path, ks))
		}
	}
	if len(c.Roles) == 0 {
		return Config{}, fmt.Errorf("config %s defines no roles", path)
	}
	for i, r := range c.Roles {
		if r.Restart != "" && !r.RestartOnFailure() {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: unknown restart mode %q (only \"on-failure\")", path, r.Name, r.Restart))
		}
		if r.Fresh && r.Start {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: fresh is for workers; the orchestrator keeps its context; fresh disabled", path, r.Name))
			c.Roles[i].Fresh = false
		}
	}
	roleNames := make(map[string]bool, len(c.Roles))
	for _, r := range c.Roles {
		roleNames[r.Name] = true
	}
	for i := range c.Roles {
		r := &c.Roles[i]
		if r.Timeout != "" {
			if d, err := time.ParseDuration(r.Timeout); err != nil || d <= 0 {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: invalid timeout %q (use a positive Go duration like \"45m\"); timeouts disabled", path, r.Name, r.Timeout))
				r.Timeout = ""
			}
		}
		if a := r.TimeoutAction; a != "" && a != "notify" && a != "restart" {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: unknown timeout_action %q (notify or restart); using notify", path, r.Name, a))
			r.TimeoutAction = ""
		}
		for _, n := range r.BaseURLEnv {
			if n == "" || strings.ContainsAny(n, "= \t") {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: invalid base_url_env name %q; using the default", path, r.Name, n))
				r.BaseURLEnv = nil
				break
			}
		}
		if r.Budget != "" {
			if v, err := strconv.ParseFloat(r.Budget, 64); err != nil || v <= 0 {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: invalid budget %q (use a positive USD amount like \"5.00\"); budget disabled", path, r.Name, r.Budget))
				r.Budget = ""
			}
		}
		if a := r.BudgetAction; a != "" && a != "notify" && a != "pause" {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: unknown budget_action %q (notify or pause); using notify", path, r.Name, a))
			r.BudgetAction = ""
		}
		if r.Judge != "" {
			if r.Judge == r.Name {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: cannot judge itself; judge disabled", path, r.Name))
				r.Judge = ""
			} else if !roleNames[r.Judge] {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: judge %q is not a configured role; judge disabled", path, r.Name, r.Judge))
				r.Judge = ""
			}
		}
		if p := r.JudgePass; p != 0 && (p < 1 || p > 10) {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: judge_pass %d out of range (1-10); using the default %d", path, r.Name, p, Role{}.JudgePassScore()))
			r.JudgePass = 0
		}
		if r.JudgeRounds < 0 {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: role %q: negative judge_rounds; using the default %d", path, r.Name, Role{}.JudgeCap()))
			r.JudgeRounds = 0
		}
	}
	th := &c.UI.Theme
	for _, e := range []struct {
		name string
		p    *string
	}{{"accent", &th.Accent}, {"working", &th.Working}, {"waiting", &th.Waiting}, {"scroll", &th.Scroll}, {"idle", &th.Idle}, {"dim", &th.Dim}} {
		if *e.p != "" && !validColor(*e.p) {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: [ui.theme] %s: invalid color %q (use ANSI 0-255 or #rrggbb); using the default", path, e.name, *e.p))
			*e.p = ""
		}
	}
	if v := c.UI.Viewer; v != "" && v != "pager" && v != "editor" {
		c.Warnings = append(c.Warnings, fmt.Sprintf("%s: [ui] viewer: unknown value %q (pager or editor); using pager", path, v))
		c.UI.Viewer = ""
	}
	c.Path = path
	c.Sphragis.applyDefaults()
	c.Keys = c.Keys.Defaulted()
	return c, nil
}
