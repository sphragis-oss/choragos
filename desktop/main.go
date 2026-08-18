// SPDX-License-Identifier: Apache-2.0

// Choragos Desktop: a read-only mirror of a running session, phase 1 of
// docs/design-macos-gui.md. It attaches over internal/wire like the TUI.
package main

import (
	"embed"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

// version must match the session server's; set via -ldflags "-X main.version=...".
var version = "dev"

// adoptLoginPath swaps in the login shell's PATH; Finder gives apps a minimal one
func adoptLoginPath() {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	out, err := exec.Command(shell, "-l", "-c", "echo -n \"$PATH\"").Output()
	if err != nil {
		slog.Warn("login shell PATH lookup failed", "shell", shell, "err", err)
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if p := lines[len(lines)-1]; strings.Contains(p, "/") {
		os.Setenv("PATH", p)
	}
}

// ensureTermEnv fills in TERM/COLORTERM, absent under Finder; agents go no-color without them
func ensureTermEnv() {
	if os.Getenv("TERM") == "" {
		os.Setenv("TERM", "xterm-256color")
	}
	if os.Getenv("COLORTERM") == "" {
		os.Setenv("COLORTERM", "truecolor")
	}
}

// desktopLogMaxBytes caps desktop.log; over it the file rotates to desktop.log.1 on start.
const desktopLogMaxBytes = 5 << 20

// openDesktopLog opens dir/desktop.log append-mode, rotating an oversized file; best-effort.
func openDesktopLog(dir string) *os.File {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	path := filepath.Join(dir, "desktop.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > desktopLogMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// setupLog tees slog to ~/Library/Logs/Choragos/desktop.log; Finder launches have no visible stderr.
func setupLog() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if f := openDesktopLog(filepath.Join(home, "Library", "Logs", "Choragos")); f != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, f), nil)))
	}
}

func main() {
	setupLog()
	slog.Info("desktop starting", "version", version, "os", runtime.GOOS+"/"+runtime.GOARCH, "go", runtime.Version(), "pid", os.Getpid())
	adoptLoginPath()
	ensureTermEnv()
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
