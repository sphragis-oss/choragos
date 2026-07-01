// SPDX-License-Identifier: Apache-2.0

// Package config loads the orchestration role set, overridable via TOML.
package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	Prompt  string   `toml:"prompt_template"`
	Start   bool     `toml:"start"`
}

// Config is the full orchestration.
type Config struct {
	Roles    []Role   `toml:"roles"`
	Sphragis Sphragis `toml:"sphragis"`
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
	if s.Enabled == nil {
		v := true
		s.Enabled = &v
	}
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
	return c
}

// Load reads the config at path, or DefaultFile in cwd, falling back to Default when absent.
func Load(path string) (Config, error) {
	if path == "" {
		if _, err := os.Stat(DefaultFile); err != nil {
			return Default(), nil
		}
		path = DefaultFile
	}
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return Config{}, fmt.Errorf("load config %s: %w", path, err)
	}
	if len(c.Roles) == 0 {
		return Config{}, fmt.Errorf("config %s defines no roles", path)
	}
	c.Sphragis.applyDefaults()
	return c, nil
}
