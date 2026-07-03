// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func workDoneCmd() *cobra.Command {
	var (
		task string
		done bool
		id   string
	)
	cmd := &cobra.Command{
		Use:     "work-done",
		Short:   "Report task completion back to the orchestrator",
		GroupID: groupControl,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(task) == "" {
				return fmt.Errorf("--task is required")
			}
			if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "work-done", Task: task, Done: done, ID: id}); err != nil {
				return fmt.Errorf("work-done failed (is the deck running?): %w", err)
			}
			cmd.Println("reported to orchestrator")
			return nil
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "summary of what was accomplished (files, outcomes)")
	cmd.Flags().BoolVar(&done, "done", false, "mark the whole assignment complete")
	cmd.Flags().StringVar(&id, "id", "", "task id from the delegation, for the task board")
	return cmd
}
