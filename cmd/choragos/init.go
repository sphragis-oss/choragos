// SPDX-License-Identifier: Apache-2.0

package main

import (
	"embed"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sphragis-oss/choragos/internal/config"
)

//go:embed templates/*.toml
var templatesFS embed.FS

// templateNames lists the embedded template names, sorted.
func templateNames() []string {
	entries, _ := templatesFS.ReadDir("templates")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
	}
	sort.Strings(names)
	return names
}

func templateBody(name string) ([]byte, error) {
	b, err := templatesFS.ReadFile("templates/" + name + ".toml")
	if err != nil {
		return nil, fmt.Errorf("unknown template %q (available: %s)", name, strings.Join(templateNames(), ", "))
	}
	return b, nil
}

func initCmd() *cobra.Command {
	var force bool
	var template string
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Write a starter " + config.DefaultFile + " in the current directory",
		Long:    "Write a starter " + config.DefaultFile + " in the current directory.\n\nTemplates: " + strings.Join(templateNames(), ", "),
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := templateBody(template)
			if err != nil {
				return err
			}
			if _, err := os.Stat(config.DefaultFile); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", config.DefaultFile)
			}
			if err := os.WriteFile(config.DefaultFile, body, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "wrote "+config.DefaultFile+" (template: "+template+")")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().StringVar(&template, "template", "starter", "config template: "+strings.Join(templateNames(), ", "))
	return cmd
}
