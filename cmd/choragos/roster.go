// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func rosterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "roster",
		Short:   "Propose changes to the running team",
		GroupID: groupControl,
	}
	cmd.AddCommand(rosterAddCmd())
	return cmd
}

func rosterAddCmd() *cobra.Command {
	var (
		name    string
		command string
		args    []string
		model   string
		promptT string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Propose adding a role to the team (the user approves it first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("--name is required")
			}
			if strings.TrimSpace(command) == "" {
				return fmt.Errorf("--command is required")
			}
			c := ipc.Command{Cmd: "roster-add", RoleName: name, RoleCommand: command,
				RoleArgs: args, RoleModel: model, RolePrompt: promptT}
			if err := ipc.Send(ipc.SocketPath(), c); err != nil {
				return fmt.Errorf("roster add failed (is the deck running?): %w", err)
			}
			cmd.Printf("proposed role %s; outcome lands in your pane\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "role name (letters, digits, - and _)")
	cmd.Flags().StringVar(&command, "command", "", "agent CLI to run (real binary on PATH)")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "argument for the command; repeatable, in order")
	cmd.Flags().StringVar(&model, "model", "", "model the agent should use")
	cmd.Flags().StringVar(&promptT, "prompt-template", "", "role brief injected at the new role's boot")
	return cmd
}
