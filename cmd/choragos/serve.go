// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/deck"
	"github.com/sphragis-oss/choragos/internal/ipc"
)

func serveCmd() *cobra.Command {
	var (
		cfgPath  string
		sphragis bool
		detach   bool
		headless bool
	)
	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Launch the orchestration deck",
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			for _, w := range cfg.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+w)
			}
			if err := cfg.CheckCommands(); err != nil {
				return err
			}
			if cmd.Flags().Changed("sphragis") {
				cfg.Sphragis.Enabled = &sphragis
			}
			if detach {
				return detachServe(cmd, cfgPath, sphragis, cmd.Flags().Changed("sphragis"))
			}
			if headless {
				return deck.RunServer(cfg, version)
			}
			return deck.Run(cfg)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "orchestration config path (default "+config.DefaultFile+", else built-in)")
	cmd.Flags().BoolVar(&sphragis, "sphragis", true, "route agent traffic through the Sphragis gateway, fail-closed (default on)")
	cmd.Flags().BoolVar(&detach, "detach", false, "start the session headless and return; reconnect with 'choragos attach'")
	cmd.Flags().BoolVar(&headless, "headless", false, "run the session server in the foreground without a TUI (used by --detach)")
	_ = cmd.Flags().MarkHidden("headless")
	return cmd
}

// detachServe double-forks the headless server for this directory and returns.
func detachServe(cmd *cobra.Command, cfgPath string, sphragis, sphragisSet bool) error {
	if err := ipc.Send(ipc.SocketPath(), ipc.Command{Cmd: "ping"}); err == nil {
		return fmt.Errorf("a session is already running for this directory (choragos attach, or choragos kill)")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"serve", "--headless"}
	if cfgPath != "" {
		abs, err := filepath.Abs(cfgPath)
		if err != nil {
			return err
		}
		args = append(args, "--config", abs)
	}
	if sphragisSet {
		args = append(args, fmt.Sprintf("--sphragis=%t", sphragis))
	}
	if err := os.MkdirAll(filepath.Join(".choragos", "logs"), 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(".choragos", "logs", "server.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	child := exec.Command(exe, args...)
	child.Stdout, child.Stderr = logf, logf
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive this terminal
	if err := child.Start(); err != nil {
		return err
	}
	pid := child.Process.Pid
	_ = child.Process.Release()
	cmd.Printf("session started (pid %d), server log at %s\nreconnect with: choragos attach\n", pid, logPath)
	return nil
}
