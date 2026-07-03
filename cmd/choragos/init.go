// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/config"
)

// starterConfig is the commented template written by `choragos init`.
const starterConfig = `# Choragos orchestration config. See docs/keybindings.md for the full keymap.

[[roles]]
name = "orchestrator"
command = "claude"
model = "opus"
start = true
prompt_template = "You coordinate the team. Plan and delegate only; never implement, review, or audit yourself."

[[roles]]
name = "coder"
command = "claude"
model = "opus"
prompt_template = "Implement the requested change. Run the project's tests before reporting done."

[[roles]]
name = "reviewer"
command = "claude"
model = "sonnet"
prompt_template = "Review the change for correctness and edge cases. Report findings only; do not modify code."
# extra status heuristics for non-Claude agent TUIs:
# input_prompts = ["continue? <enter>"]
# chrome_markers = ["my statusbar"]

# [sphragis]
# enabled = true        # route agent LLM traffic through the gateway
# fail_closed = true    # refuse delegation when the gateway is down
# addr = "127.0.0.1:8787"

# [keys]                # tmux-style prefix bindings (herdr defaults)
# prefix = "ctrl+b"
# split_vertical = "v"
# split_horizontal = "-"
# close_pane = "x"
# zoom = "z"
# resize_mode = "r"
# toggle_sidebar = "b"
# restart_role = "R"
# broadcast = "a"
# task_board = "t"
# search = "/"
# help = "?"

# [ui]
# auto_focus = true     # focus whichever agent produces output
# sidebar = true        # start with the status-card sidebar visible
# bell = true           # terminal bell when an agent blocks on input
`

func initCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Write a starter " + config.DefaultFile + " in the current directory",
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := os.Stat(config.DefaultFile); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", config.DefaultFile)
			}
			if err := os.WriteFile(config.DefaultFile, []byte(starterConfig), 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "wrote "+config.DefaultFile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	return cmd
}
