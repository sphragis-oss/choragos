// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

func lsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List running choragos sessions",
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			metas := ipc.ReadMetas()
			alive := 0
			for _, m := range metas {
				if ipc.Send(m.Socket, ipc.Command{Cmd: "ping"}) != nil {
					_ = os.Remove(filepath.Join(ipc.SessionDir(), ipc.SessionID(m.Dir)+".json")) // stale crash leftover
					continue
				}
				alive++
				cmd.Printf("%s  pid %-6d  up %-8s  %s\n",
					ipc.SessionID(m.Dir), m.PID, time.Since(m.Started).Round(time.Second), m.Dir)
			}
			if alive == 0 {
				cmd.Println("no running sessions")
			}
			return nil
		},
	}
}

func killCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "kill",
		Short:   "Stop this directory's session (agents included)",
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if all {
				n := 0
				for _, m := range ipc.ReadMetas() {
					if ipc.Send(m.Socket, ipc.Command{Cmd: "shutdown"}) == nil {
						cmd.Printf("stopped %s (%s)\n", ipc.SessionID(m.Dir), m.Dir)
						n++
					}
				}
				if n == 0 {
					cmd.Println("no running sessions")
				}
				return nil
			}
			if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "shutdown"}); err != nil {
				return fmt.Errorf("no session running for this directory")
			}
			cmd.Println("session stopped")
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "stop every running session")
	return cmd
}
