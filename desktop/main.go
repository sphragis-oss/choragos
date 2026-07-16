// SPDX-License-Identifier: Apache-2.0

// Choragos Desktop: a read-only mirror of a running session, phase 1 of
// docs/design-macos-gui.md. It attaches over internal/wire like the TUI.
package main

import (
	"embed"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

// version must match the session server's; set via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app := newApp(version)
	err := wails.Run(&options.App{
		Title:  "Choragos",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind:       []any{app},
		Mac: &mac.Options{
			Appearance: mac.NSAppearanceNameDarkAqua,
		},
	})
	if err != nil {
		slog.Error("wails run failed", "err", err)
		os.Exit(1)
	}
}
