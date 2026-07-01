// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestRootCmdWiring(t *testing.T) {
	root := rootCmd()
	if root.Use != "choragos" {
		t.Fatalf("root Use = %q, want choragos", root.Use)
	}
	want := map[string]bool{"serve": false, "delegate": false, "work-done": false, "version": false}
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
