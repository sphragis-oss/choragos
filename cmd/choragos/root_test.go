// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

func TestRootCmdWiring(t *testing.T) {
	root := rootCmd()
	if root.Use != "choragos" {
		t.Fatalf("root Use = %q, want choragos", root.Use)
	}
	want := map[string]bool{"serve": false, "init": false, "delegate": false, "work-done": false, "version": false}
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
