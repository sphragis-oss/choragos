// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// genManCmd is release tooling: writes the man page tree for packaging.
func genManCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "gen-man <dir>",
		Short:  "Generate man pages into <dir> (release tooling)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := os.MkdirAll(args[0], 0o755); err != nil {
				return err
			}
			header := &doc.GenManHeader{Title: "CHORAGOS", Section: "1", Source: "choragos " + version, Manual: "Choragos Manual"}
			return doc.GenManTree(rootCmd(), header, args[0])
		},
	}
}
