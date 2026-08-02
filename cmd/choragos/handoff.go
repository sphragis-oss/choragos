// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
)

func handoffCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:     "handoff",
		Short:   "End the session with a handoff document for the next one",
		Long:    "Ask the running deck's orchestrator to write .choragos/handoff-session.md\n(goal, state of the work, in-flight and remaining tasks), then stop the\nsession. The next `choragos serve --resume` restores the board and attaches\nthe document to the new orchestrator's boot context.\n\n--config names the team config the next session should resume with; roles\nit drops become tombstones instead of refusing the resume.",
		GroupID: groupControl,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := ipc.Command{Cmd: "handoff"}
			resumeHint := "choragos serve --resume"
			if cfgPath != "" {
				abs, err := filepath.Abs(cfgPath)
				if err != nil {
					return err
				}
				if _, err := config.Load(abs); err != nil {
					return fmt.Errorf("next config: %w", err)
				}
				c.NextConfig = abs
				resumeHint += " --config " + abs
			}
			if err := ipc.Send(ipc.SocketPath(), c); err != nil {
				return fmt.Errorf("handoff failed (is the deck running?): %w", err)
			}
			cmd.Println("handoff requested; the orchestrator is writing .choragos/handoff-session.md\nand the session will stop. Resume with: " + resumeHint)
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "team config for the next session (defaults to the current one)")
	return cmd
}
