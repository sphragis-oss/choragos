// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/deck"
)

func attachCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "attach",
		Short:   "Attach the deck TUI to this directory's detached session",
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return deck.RunAttach(version)
		},
	}
}
