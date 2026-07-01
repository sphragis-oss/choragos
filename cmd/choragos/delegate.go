// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func delegateCmd() *cobra.Command {
	var (
		to   []string
		task string
	)
	cmd := &cobra.Command{
		Use:     "delegate",
		Short:   "Delegate a task to one or more roles",
		GroupID: groupControl,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(to) == 0 {
				return fmt.Errorf("--to is required")
			}
			if strings.TrimSpace(task) == "" {
				return fmt.Errorf("--task is required")
			}
			if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "delegate", To: to, Task: task}); err != nil {
				return fmt.Errorf("delegate failed (is the deck running?): %w", err)
			}
			cmd.Printf("delegated to %s\n", strings.Join(to, ", "))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&to, "to", nil, "target role(s); repeat for parallel delegation")
	cmd.Flags().StringVar(&task, "task", "", "full task with context, file paths, and constraints")
	return cmd
}
