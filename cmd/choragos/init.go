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

//go:embed templates/*.toml templates/auto/*.toml
var templatesFS embed.FS

// templateNames lists the embedded template names, sorted.
func templateNames() []string {
	entries, _ := templatesFS.ReadDir("templates")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue // templates/auto belongs to --auto, not --template
		}
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
	var force, auto bool
	var template string
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Write a starter " + config.DefaultFile + " in the current directory",
		Long:    "Write a starter " + config.DefaultFile + " in the current directory.\n\nTemplates: " + strings.Join(templateNames(), ", ") + "\n\n--auto detects the project (go.mod, package.json, Cargo.toml, pyproject.toml, ...)\nand writes a team with language-specific roles instead.",
		GroupID: groupDeck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, label, err := initBody(cmd, template, auto)
			if err != nil {
				return err
			}
			if _, err := os.Stat(config.DefaultFile); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", config.DefaultFile)
			}
			if err := os.WriteFile(config.DefaultFile, body, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "wrote "+config.DefaultFile+" ("+label+")")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().BoolVar(&auto, "auto", false, "detect the project and write a language-specific team")
	cmd.Flags().StringVar(&template, "template", "starter", "config template: "+strings.Join(templateNames(), ", "))
	cmd.MarkFlagsMutuallyExclusive("auto", "template")
	return cmd
}

// initBody picks the config to write: an explicit template, or the detected team for --auto.
func initBody(cmd *cobra.Command, template string, auto bool) (body []byte, label string, err error) {
	if !auto {
		body, err = templateBody(template)
		return body, "template: " + template, err
	}
	dominant, others := detectProject(".")
	if dominant == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "no project manifest detected; writing the starter template")
		body, err = templateBody("starter")
		return body, "template: starter", err
	}
	body, err = templatesFS.ReadFile("templates/auto/" + dominant + ".toml")
	label = "auto: " + dominant
	if len(others) > 0 {
		label += "; also detected: " + strings.Join(others, ", ")
		note := "# Also detected: " + strings.Join(others, ", ") + ". This team targets the dominant\n# language by source count; add roles for the others as needed.\n"
		body = append([]byte(note), body...)
	}
	return body, label, err
}
