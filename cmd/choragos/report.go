// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/deck"
)

func reportCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "report [events.log]",
		Short:   "Summarize a run's per-role activity from the event log",
		Long:    "Aggregate .choragos/logs/events.log (or the given file) into a per-role\ntable: tasks handled, completions, busy and average task time, first and\nlast activity, and token usage. Tokens come from periodic gateway\nsnapshots; when the gateway was off the column reads n/a.",
		GroupID: groupDeck,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := filepath.Join(".choragos", "logs", "events.log")
			if len(args) == 1 {
				path = args[0]
			}
			return deck.Report(path, cmd.OutOrStdout())
		},
	}
}
