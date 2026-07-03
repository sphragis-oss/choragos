// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

func TestRootCmdWiring(t *testing.T) {
	root := rootCmd()
	if root.Use != "choragos" {
		t.Fatalf("root Use = %q, want choragos", root.Use)
	}
	want := map[string]bool{"serve": false, "init": false, "doctor": false, "delegate": false, "work-done": false, "version": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestVersionNotEmpty(t *testing.T) {
	if version == "" {
		t.Fatal("version must not be empty")
	}
}

func TestDoctorChecks(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CHORAGOS_SOCK", "/tmp/doctor-test.sock")
	body := `[[roles]]
name = "solo"
command = "sh"
start = true

[sphragis]
enabled = false

[ui]
auto_focsu = true
`
	if err := os.WriteFile("c.toml", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if fails := runDoctor(&out, "c.toml"); fails != 0 {
		t.Fatalf("healthy setup should pass, got %d fails:\n%s", fails, out.String())
	}
	for _, want := range []string{"OK    config", "OK    role:solo", "WARN  config", "auto_focsu", "WARN  sphragis"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor output missing %q:\n%s", want, out.String())
		}
	}
	// a role command that cannot resolve fails the run
	bad := strings.Replace(body, `command = "sh"`, `command = "definitely-not-a-binary-xyz"`, 1)
	if err := os.WriteFile("bad.toml", []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if fails := runDoctor(&out, "bad.toml"); fails == 0 {
		t.Fatalf("missing role binary should fail:\n%s", out.String())
	}
	// an over-long socket path fails
	t.Setenv("CHORAGOS_SOCK", "/tmp/"+strings.Repeat("x", 120)+".sock")
	out.Reset()
	if fails := runDoctor(&out, "c.toml"); fails == 0 {
		t.Fatalf("long socket path should fail:\n%s", out.String())
	}
}

func TestGenManWritesTree(t *testing.T) {
	dir := t.TempDir()
	cmd := genManCmd()
	if err := cmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"choragos.1", "choragos-serve.1", "choragos-doctor.1"} {
		if _, err := os.Stat(dir + "/" + want); err != nil {
			t.Errorf("missing man page %s: %v", want, err)
		}
	}
}

func TestInitWritesLoadableConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	cmd := initCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), config.DefaultFile) {
		t.Errorf("init output = %q", out.String())
	}
	c, err := config.Load("")
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if len(c.Roles) != 3 || !c.Roles[0].Start {
		t.Fatalf("unexpected starter roles: %+v", c.Roles)
	}
	// refuses to clobber without --force
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("second init should fail without --force")
	}
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("init --force failed: %v", err)
	}
}

func TestInitTemplates(t *testing.T) {
	names := templateNames()
	if len(names) < 4 {
		t.Fatalf("expected at least 4 templates, got %v", names)
	}
	// every embedded template must produce a loadable config
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			cmd := initCmd()
			cmd.SetOut(&strings.Builder{})
			if err := cmd.Flags().Set("template", name); err != nil {
				t.Fatal(err)
			}
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
			c, err := config.Load("")
			if err != nil {
				t.Fatalf("template %s does not load: %v", name, err)
			}
			if len(c.Roles) == 0 {
				t.Fatalf("template %s has no roles", name)
			}
			if len(c.Warnings) != 0 {
				t.Fatalf("template %s has config warnings: %v", name, c.Warnings)
			}
		})
	}
	// unknown template errors and names the available ones
	t.Chdir(t.TempDir())
	cmd := initCmd()
	cmd.SetOut(&strings.Builder{})
	if err := cmd.Flags().Set("template", "nope"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "starter") {
		t.Fatalf("unknown template error should list available templates, got: %v", err)
	}
}
