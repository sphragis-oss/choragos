// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// errSilent suppresses the top-level error print; the command already reported.
var errSilent = errors.New("silent")

const (
	groupDeck    = "deck"
	groupControl = "control"
	groupOther   = "other"
)

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "choragos",
		Short:         "Secure multi-agent dev orchestrator",
		Long:          "Choragos - secure multi-agent development orchestrator.\n\nRuns a team of AI coding agents in owned PTY panes and routes every\nagent's LLM traffic through the Sphragis compliance gateway (local PII\nredaction plus a tamper-evident, hash-chained audit log).",
		Example:       "  choragos serve                            # launch the deck\n  choragos delegate --to coder --task \"...\"  # hand work to a role\n  choragos work-done --task \"...\"            # report completion",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       version,
	}
	root.SetVersionTemplate("choragos {{.Version}}\n")
	root.AddGroup(
		&cobra.Group{ID: groupDeck, Title: "Deck Commands:"},
		&cobra.Group{ID: groupControl, Title: "Control Commands:"},
		&cobra.Group{ID: groupOther, Title: "Other Commands:"},
	)
	root.SetHelpCommandGroupID(groupOther)
	root.SetCompletionCommandGroupID(groupOther)
	root.AddCommand(
		serveCmd(),
		initCmd(),
		doctorCmd(),
		delegateCmd(),
		workDoneCmd(),
		versionCmd(),
		genManCmd(),
	)
	return root
}
