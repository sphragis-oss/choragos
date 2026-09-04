// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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

func TestDetectInfraProjects(t *testing.T) {
	dir := t.TempDir()
	// a charts/<name> monorepo has no root Chart.yaml; the glob manifest finds it
	touch(t, dir, "charts/foo/Chart.yaml", "charts/foo/templates/deployment.yaml", "charts/foo/templates/_helpers.tpl")
	if d, others := detectProject(dir); d != "helm" || len(others) != 0 {
		t.Fatalf("charts layout = %q %v", d, others)
	}
	// terragrunt joins; helm stays dominant by source count
	touch(t, dir, "terragrunt.hcl", "live/prod/terragrunt.hcl")
	d, others := detectProject(dir)
	if d != "helm" || len(others) != 1 || others[0] != "terraform" {
		t.Fatalf("mixed = %q %v", d, others)
	}
	touch(t, dir, "modules/vpc/main.tf", "modules/vpc/variables.tf", "modules/vpc/outputs.tf", "live/prod/vpc.tfvars")
	if d, _ := detectProject(dir); d != "terraform" {
		t.Fatalf("terraform-heavy = %q", d)
	}

	root := t.TempDir()
	touch(t, root, "Chart.yaml", "values.yaml", "templates/svc.yaml")
	if d, _ := detectProject(root); d != "helm" {
		t.Fatalf("root chart = %q", d)
	}
}

func TestInfraTemplatesCarryCheck(t *testing.T) {
	for name, want := range map[string]string{"terraform": "terraform validate", "helm": "helm unittest --failfast"} {
		t.Chdir(t.TempDir())
		body, err := templatesFS.ReadFile("templates/auto/" + name + ".toml")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.DefaultFile, body, 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := config.Load("")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.Roles[1].Check, want) {
			t.Errorf("%s coder check = %q, want it to contain %q", name, c.Roles[1].Check, want)
		}
	}
}

func TestHelmTemplateCheckHandlesBothLayouts(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	body, err := templatesFS.ReadFile("templates/auto/helm.toml")
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if _, err := toml.Decode(string(body), &c); err != nil {
		t.Fatal(err)
	}
	// a fake helm on PATH records which chart dirs the check visited
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte("#!/bin/sh\necho \"$@\" >> \"$HELM_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(root string) string {
		log := filepath.Join(t.TempDir(), "log")
		cmd := exec.Command("sh", "-c", c.Roles[1].Check)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "HELM_LOG="+log)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("check failed in %s: %v\n%s", root, err, out)
		}
		got, _ := os.ReadFile(log)
		return string(got)
	}
	mono := t.TempDir()
	touch(t, mono, "charts/a/Chart.yaml", "charts/b/Chart.yaml", "charts/README.md")
	if got := run(mono); !strings.Contains(got, "unittest --failfast charts/a/") || !strings.Contains(got, "unittest --failfast charts/b/") || strings.Contains(got, " .\n") {
		t.Fatalf("monorepo visits = %q", got)
	}
	root := t.TempDir()
	touch(t, root, "Chart.yaml")
	if got := run(root); !strings.Contains(got, "unittest --failfast .") {
		t.Fatalf("root chart visits = %q", got)
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
