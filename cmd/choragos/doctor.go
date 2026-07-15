// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/checkpoint"
	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
	"github.com/sphragis-oss/choragos/internal/sphragis"
)

// maxSocketPath is a portable bound for sun_path (104 on darwin, 108 on linux).
const maxSocketPath = 100

func doctorCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:     "doctor",
		Short:   "Check the environment for common problems before serving",
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fails := runDoctor(cmd.OutOrStdout(), cfgPath)
			if fails > 0 {
				return fmt.Errorf("%d check(s) failed", fails)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "orchestration config path (default "+config.DefaultFile+", else built-in)")
	return cmd
}

// runDoctor prints one line per check and returns the number of failures.
func runDoctor(out io.Writer, cfgPath string) int {
	fails := 0
	report := func(level, name, msg string) {
		if level == "FAIL" {
			fails++
		}
		fmt.Fprintf(out, "%-4s  %-14s %s\n", level, name, msg)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		report("FAIL", "config", err.Error())
		return fails
	}
	report("OK", "config", fmt.Sprintf("%d role(s) loaded", len(cfg.Roles)))
	for _, w := range cfg.Warnings {
		report("WARN", "config", w)
	}

	for _, r := range cfg.Roles {
		if _, err := exec.LookPath(r.Command); err != nil {
			report("FAIL", "role:"+r.Name, fmt.Sprintf("command %q not found in PATH (shell aliases do not resolve)", r.Command))
		} else {
			report("OK", "role:"+r.Name, r.Command)
		}
	}

	sock := ipc.SocketPath()
	switch {
	case len(sock) > maxSocketPath:
		report("FAIL", "socket", fmt.Sprintf("%s is %d chars; unix sockets cap at ~104, set CHORAGOS_SOCK to a shorter path", sock, len(sock)))
	default:
		if f, err := os.CreateTemp(filepath.Dir(sock), ".choragos-doctor-*"); err != nil {
			report("FAIL", "socket", fmt.Sprintf("socket dir not writable: %v", err))
		} else {
			_ = f.Close()
			_ = os.Remove(f.Name())
			report("OK", "socket", sock)
		}
	}

	term := os.Getenv("TERM")
	switch term {
	case "", "dumb":
		report("FAIL", "terminal", fmt.Sprintf("TERM=%q; the deck needs an interactive terminal", term))
	default:
		report("OK", "terminal", "TERM="+term)
	}

	if cfg.Sphragis.IsEnabled() {
		if sphragis.Healthy(cfg.Sphragis.Addr) {
			report("OK", "sphragis", "gateway already healthy at "+cfg.Sphragis.Addr)
		} else if _, err := exec.LookPath(cfg.Sphragis.Command); err == nil {
			report("OK", "sphragis", cfg.Sphragis.Command+" in PATH; serve will start the gateway")
		} else if cfg.Sphragis.Enabled == nil {
			report("WARN", "sphragis", fmt.Sprintf("%q not in PATH; serve will run with the gateway off (set [sphragis] enabled = true to require it)", cfg.Sphragis.Command))
		} else {
			report("FAIL", "sphragis", fmt.Sprintf("gateway enabled but %q not in PATH (serve would fail closed)", cfg.Sphragis.Command))
		}
	} else {
		report("WARN", "sphragis", "gateway disabled; agent traffic is not routed or audited")
	}

	if !cfg.Checkpoints.IsEnabled() {
		report("WARN", "checkpoints", "disabled in config; delegations will not be snapshotted")
	} else if ok, reason := checkpoint.New(".").Active(); ok {
		report("OK", "checkpoints", "git repository; delegations snapshot the workspace")
	} else {
		report("WARN", "checkpoints", reason+"; delegations will not be snapshotted")
	}
	return fails
}
