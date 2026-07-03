# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `choragos init --template <name>`: embedded team templates (`starter`,
  `claude-team`, `mixed-team`, `review`).

## [0.2.0] - 2026-07-03

### Added
- Tiling window manager over the role panes, tmux-style behind a configurable
  prefix (default `ctrl+b`): splits, directional focus, cycle, zoom, live
  resize, close-without-kill, sidebar toggle, help overlay (`prefix+?`).
- `[keys]` table in `.choragos.toml` (herdr-compatible values) and `[ui]`
  options: `auto_focus`, `sidebar`, `bell`.
- Role restart in place (`prefix+R`); the agent respawns at the tile size.
- Broadcast input mode (`prefix+a`): keys go to every live pane.
- Task board (`prefix+t`): delegations get ids (`T<n>`), workers echo them via
  `work-done --id`, and the board shows pending/done with durations.
- Scrollback search (`prefix+/`, `n`/`N`), mouse click-to-focus and wheel
  scrollback, terminal bell when an agent blocks waiting for input.
- Per-role environment isolation: `env_allow` (baseline plus allowlist) and
  `env_deny` (strip patterns), so credentials only reach roles that need them.
- Per-role status heuristics (`input_prompts`, `chrome_markers`) for
  non-Claude agent TUIs; boot injection is now verified on screen and retried.
- `choragos init` (commented starter config) and `choragos doctor`
  (config typos, role binaries, socket, TERM, gateway checks).
- Crash hygiene: panics restore the terminal, stop the agents, and write
  `.choragos/logs/crash.log`.
- Unknown TOML keys produce warnings instead of failing silently.
- Releases ship bash/zsh/fish completions and man pages; SECURITY.md documents
  cosign/checksum/attestation verification.
- Tracked documentation: keybindings, configuration reference,
  troubleshooting, long-running sessions; README demo GIF.
- CI: end-to-end tmux smoke test, fuzz targets, coverage upload.

### Changed
- The deck layout replaced the auto-expanding accordion: multiple role panes
  render simultaneously as tiles, and every visible tile is resized live.
- The README least-privilege claim now matches the implementation (env
  isolation is opt-in per role).

## [0.1.0] - 2026-07-02

### Added
- Initial project scaffold: modules, cobra CLI, config loading.
- Bubble Tea TUI with a status-card column and an auto-expanding accordion layout.
- PTY pane management allowing real-time injection and capture of agent activity via `vt10x`.
- IPC via UNIX socket (0600) enabling inter-agent `delegate` and `work-done` flows.
- Sphragis gateway supervisor mapping LLM traffic implicitly into a local AI Act compliance layer.
- `Orchestrator`, `Coder`, `Reviewer`, `Auditor`, and `Release` default crew setups via TOML config.

[Unreleased]: https://github.com/sphragis-oss/choragos/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/sphragis-oss/choragos/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sphragis-oss/choragos/releases/tag/v0.1.0
