// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func reloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "reload",
		Short:   "Ask the running deck to reload its config (add/remove roles live)",
		GroupID: groupControl,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "reload"}); err != nil {
				return fmt.Errorf("reload failed (is the deck running?): %w", err)
			}
			cmd.Println("reload requested; outcome in .choragos/logs/events.log")
			return nil
		},
	}
}
