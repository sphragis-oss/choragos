// SPDX-License-Identifier: Apache-2.0

package main

import "github.com/spf13/cobra"

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the version",
		GroupID: groupOther,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("choragos %s\n", version)
			return nil
		},
	}
}
