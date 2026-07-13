// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/checkpoint"
	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
)

// activeStore returns a ready checkpoint store for the current directory or a clear error.
func activeStore() (*checkpoint.Store, error) {
	st := checkpoint.New(".")
	if ok, reason := st.Active(); !ok {
		return nil, fmt.Errorf("checkpoints unavailable: %s", reason)
	}
	return st, nil
}

func checkpointsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "checkpoints",
		Short:   "List this directory's workspace checkpoints",
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := activeStore()
			if err != nil {
				return err
			}
			entries, err := st.List()
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				cmd.Println("no checkpoints")
				return nil
			}
			for _, e := range entries {
				cmd.Printf("%-18s %-9s %s\n", e.TaskID, humanizeAge(time.Since(e.At)), e.Subject)
			}
			return nil
		},
	}
	cmd.AddCommand(pruneCmd())
	return cmd
}

func pruneCmd() *cobra.Command {
	var keep int
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete all but the newest checkpoints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := activeStore()
			if err != nil {
				return err
			}
			if keep <= 0 {
				cfg, err := config.Load("")
				if err != nil {
					return err
				}
				keep = cfg.Checkpoints.KeepCount()
			}
			n, err := st.Prune(keep)
			if err != nil {
				return err
			}
			cmd.Printf("pruned %d checkpoint(s), kept the newest %d\n", n, keep)
			return nil
		},
	}
	cmd.Flags().IntVar(&keep, "keep", 0, "checkpoints to retain (default from [checkpoints] keep)")
	return cmd
}

func rollbackCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rollback <task-id>",
		Short:   "Restore the workspace to the state before a task ran",
		Long:    "Restore tracked and untracked files to the checkpoint taken before <task-id>\nwas delegated. History is never touched: HEAD, branches, the index, the stash,\nand any commits a worker created all stay as they are. The current state is\ncheckpointed first, so a rollback is itself undoable.",
		GroupID: groupDeck,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := activeStore()
			if err != nil {
				return err
			}
			entries, err := st.List()
			if err != nil {
				return err
			}
			target, ok := findCheckpoint(entries, args[0])
			if !ok {
				return fmt.Errorf("no checkpoint for %q (see: choragos checkpoints)", args[0])
			}
			if ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "ping"}) == nil {
				cmd.Println("warning: a session is running in this directory; in-flight workers may write concurrently")
			}
			pre, err := st.Snapshot(fmt.Sprintf("%d-pre-rollback-%s", time.Now().Unix(), target.TaskID),
				"pre-rollback -> "+target.Name, "head: "+st.Head())
			if err != nil {
				return err
			}
			changed, extra, err := st.Diff(target.Ref, pre)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				cmd.Printf("workspace already matches checkpoint %s\n", target.Name)
				return nil
			}
			if head := st.MetaHead(target.Ref); head != "" && head != st.Head() {
				cmd.Printf("note: HEAD has moved since this checkpoint (%.8s -> %.8s); files are restored, history is untouched\n", head, st.Head())
			}
			cmd.Printf("rolling back to %s (%s): %d file(s) restored, %d deleted\n",
				target.Name, target.Subject, len(changed)-len(extra), len(extra))
			if !yes && !confirm(cmd) {
				cmd.Println("aborted; nothing changed")
				return nil
			}
			if err := st.Apply(target.Ref, extra); err != nil {
				return err
			}
			cmd.Printf("done; undo with: choragos rollback pre-rollback-%s\n", target.TaskID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// findCheckpoint returns the newest checkpoint matching a task id, name, or full ref.
func findCheckpoint(entries []checkpoint.Entry, arg string) (checkpoint.Entry, bool) {
	for _, e := range entries { // List is newest first
		if e.TaskID == arg || e.Name == arg || e.Ref == arg {
			return e, true
		}
	}
	return checkpoint.Entry{}, false
}

// confirm asks for an explicit y before a destructive action.
func confirm(cmd *cobra.Command) bool {
	cmd.Print("proceed? [y/N] ")
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// humanizeAge formats an age like the deck's status labels: 5s, 3m, 2h, 4d.
func humanizeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
