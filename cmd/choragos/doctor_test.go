// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorWarnsOnSameVendorJudge(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "orchestrator"
command = "cat"
start = true

[[roles]]
name = "coder"
command = "cat"
judge = "reviewer"

[[roles]]
name = "reviewer"
command = "cat"
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runDoctor(&out, f) // overall failures depend on the environment (TERM, PATH); only the WARN matters here
	if !strings.Contains(out.String(), "judge:coder") || !strings.Contains(out.String(), "self-agree") {
		t.Fatalf("same-vendor judge WARN missing:\n%s", out.String())
	}
}

func TestDoctorWarnsOnModelForUnknownCLI(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "coder"
command = "cat"
model = "opus"
start = true
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runDoctor(&out, f)
	if !strings.Contains(out.String(), "not a known model-aware CLI") {
		t.Fatalf("model_flag WARN missing:\n%s", out.String())
	}
}

func TestDoctorQuietOnModelFlagOptOut(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "orchestrator"
command = "claude"
model = "opus"
start = true

[[roles]]
name = "coder"
command = "cat"
model = "opus"
model_flag = ""
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runDoctor(&out, f)
	if strings.Contains(out.String(), "model-aware") {
		t.Fatalf("unexpected model_flag WARN for known CLI or explicit opt-out:\n%s", out.String())
	}
}

func TestDoctorQuietOnCrossVendorJudge(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.toml")
	body := `[[roles]]
name = "coder"
command = "cat"
start = true
judge = "reviewer"

[[roles]]
name = "reviewer"
command = "sh"
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runDoctor(&out, f)
	if strings.Contains(out.String(), "judge:") {
		t.Fatalf("unexpected judge WARN for cross-vendor pair:\n%s", out.String())
	}
}
