// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

func touch(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectProject(t *testing.T) {
	dir := t.TempDir()
	if d, _ := detectProject(dir); d != "" {
		t.Fatalf("empty dir detected %q", d)
	}
	touch(t, dir, "go.mod", "main.go", "internal/a.go")
	if d, others := detectProject(dir); d != "go" || len(others) != 0 {
		t.Fatalf("go project = %q %v", d, others)
	}
	// node manifest joins, but go stays dominant by source count
	touch(t, dir, "package.json", "web/app.ts")
	d, others := detectProject(dir)
	if d != "go" || len(others) != 1 || others[0] != "node" {
		t.Fatalf("mixed = %q %v", d, others)
	}
	// node takes over when its sources outnumber go's
	touch(t, dir, "web/b.ts", "web/c.tsx", "web/d.js")
	if d, _ := detectProject(dir); d != "node" {
		t.Fatalf("node-heavy = %q", d)
	}
	// dependency trees do not count
	touch(t, dir, "node_modules/dep/e.js", "node_modules/dep/f.js", "node_modules/dep/g.js", "node_modules/dep/h.js", "vendor/v.go")
	if d, _ := detectProject(dir); d != "node" {
		t.Fatalf("after skipped dirs = %q", d)
	}
}

func TestInitAutoWritesLanguageTeam(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	touch(t, dir, "go.mod", "main.go", "package.json")
	cmd := initCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Flags().Set("auto", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auto: go; also detected: node") {
		t.Fatalf("output = %q", out.String())
	}
	c, err := config.Load("")
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if len(c.Roles) != 3 || !c.Roles[0].Start {
		t.Fatalf("roles = %+v", c.Roles)
	}
	if !strings.Contains(c.Roles[1].Prompt, "go test") {
		t.Fatalf("coder prompt not Go-specific: %q", c.Roles[1].Prompt)
	}
	raw, _ := os.ReadFile(config.DefaultFile)
	if !strings.Contains(string(raw), "# Also detected: node") {
		t.Fatalf("multi-language note missing:\n%s", raw)
	}
}

func TestInitAutoFallsBackToStarter(t *testing.T) {
	t.Chdir(t.TempDir())
	cmd := initCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Flags().Set("auto", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no project manifest detected") || !strings.Contains(out.String(), "template: starter") {
		t.Fatalf("output = %q", out.String())
	}
	if _, err := config.Load(""); err != nil {
		t.Fatalf("fallback config does not load: %v", err)
	}
}

func TestAutoTemplatesLoadable(t *testing.T) {
	for _, l := range autoLanguages {
		t.Run(l.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			body, err := templatesFS.ReadFile("templates/auto/" + l.name + ".toml")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config.DefaultFile, body, 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := config.Load("")
			if err != nil {
				t.Fatalf("auto template %s does not load: %v", l.name, err)
			}
			if len(c.Roles) != 3 || !c.Roles[0].Start || len(c.Warnings) != 0 {
				t.Fatalf("roles=%d warnings=%v", len(c.Roles), c.Warnings)
			}
		})
	}
}
