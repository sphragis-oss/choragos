// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/deck"
)

func serveCmd() *cobra.Command {
	var (
		cfgPath  string
		sphragis bool
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
			return deck.Run(cfg)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "orchestration config path (default "+config.DefaultFile+", else built-in)")
	cmd.Flags().BoolVar(&sphragis, "sphragis", true, "route agent traffic through the Sphragis gateway, fail-closed (default on)")
	return cmd
}
