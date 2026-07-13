# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.0] - 2026-07-13

### Added
- Native detach/attach: `choragos serve --detach` runs the crew headless
  in the background, `choragos attach` brings the TUI back with screens,
  tasks, gates, and the tiling layout restored, and `prefix+d` (default)
  detaches again leaving the agents running. Attach replay is exact
  (sequence-numbered ring buffer, no duplicated or lost bytes), one
  client attaches at a time, and a client/server version mismatch is
  refused with a clear message. While detached, delegations, approval
  gates, auto-restart, and notification hooks keep working.
- Session management across projects: sessions are per working
  directory (socket under `<runtime dir>/choragos-<uid>/`), `choragos ls`
  lists them with liveness, and `choragos kill [--all]` stops them; two
  new field-less IPC verbs `ping` and `shutdown` back these and are
  documented in docs/protocol.md.
- docs/long-running-sessions.md rewritten around native detach/attach;
  tmux is now the fallback, with a note on the prefix-key collision.

### Changed
- The deck internals split into a UI-free session core and the TUI
  model (behavior-neutral refactor), which is what lets the same
  session run headless under the server or rendered under the client.

### Added
- Roles at runtime: edit the config file, then `choragos reload` (new
  IPC verb) or `prefix+C`, and the deck converges the team by role name.
  Added roles spawn and get their boot brief, removed roles stop
  gracefully and leave the sidebar and delegation targets, a changed
  `command`/`args`/`model`/`env_*` respawns that role in place, and
  prompt/approve/restart changes apply without a restart. The start
  role's process is never touched, roles with in-flight work are not
  respawned, and the orchestrator is told the roster changed.
- Edit the brief from the approval overlay: when a gated delegation
  carries a brief, `e` suspends the deck, opens the brief in
  `$VISUAL`/`$EDITOR` (fallback `vi`), and returns to the overlay; the
  gate stays pending until `y` or `n`.
- Notification hooks: `[ui] on_gate` and `[ui] on_input` run a command
  (via `sh -c`, in the background, `CHORAGOS_ROLE`/`CHORAGOS_TASK` in
  the env) when a delegation awaits approval or an agent blocks on
  input, so the deck can reach you when the terminal bell cannot. A
  failing hook is logged and otherwise ignored.
- `docs/design-session-server.md`: the design pass for runtime roles
  and detach/attach; this release ships its v0.6 stage (tombstones +
  reload), the session/UI split is staged for v0.7.

## [0.5.0] - 2026-07-12

### Added
- Human approval gates: set `approve = true` on a role and every
  delegation to it pauses in the deck. A modal overlay shows the target,
  task, and brief path; `y` injects it through the normal delivery path,
  `n` rejects and tells the orchestrator to revise. Gates queue in
  arrival order, the status line counts them, and briefs being files
  means you can edit the brief and then approve. Approvals and
  rejections land in events.log.
- Role supervision: `restart = "on-failure"` respawns a role in place
  when its process exits non-zero or dies by signal, capped by
  `restart_retries` (default 3) so a broken command cannot crash-loop.
  Clean exits and deck shutdown are respected, a manual `prefix+R`
  resets the budget, and every attempt is logged.

## [0.4.2] - 2026-07-12

### Added
- Context hygiene: the embedded `init` templates start claude worker roles
  with `--strict-mcp-config`, so delegated workers stop inheriting your
  personal MCP servers (~25k tokens per role in a reported case). A new
  "Context hygiene" section in docs/teams.md explains the `/context`
  breakdown and which flag trims what.

### Fixed
- Role logs are readable: `.choragos/logs/<role>.log` now holds the
  plain-text transcript of what the pane showed (rendered scrollback,
  written when the pane closes) instead of the raw PTY escape-sequence
  stream. Logs append across role restarts, and each session starts with
  a header carrying the role, working directory, and start time; the
  `deck starting` event records the working directory too.

## [0.4.1] - 2026-07-12

### Fixed
- Auto-focus no longer fires on every output frame: agent CLIs redraw
  spinners continuously, so the visible tile retargeted several times per
  second until a manual focus action paused auto-focus for the session.
  Focus now follows the workflow only: delegations, work-done reports, and
  a pane transitioning into waiting-for-input.

## [0.4.0] - 2026-07-11

### Added
- Brief-file delegation: `choragos delegate --brief <path>` hands a worker a
  task file (objective, acceptance criteria, references); `--task` becomes an
  optional short label. `choragos work-done --report <path>` points the
  orchestrator at a full report. The CLI validates and absolutizes both
  paths, the task board shows the attached file per entry, and the built-in
  prompts teach both flags.
- `[ui] mouse = false`: disable mouse capture entirely and restore
  terminal-native text selection; tile focus and wheel scrollback fall back
  to the keyboard bindings.
- `docs/protocol.md`: the delegate/work-done wire contract for integrators
  (socket resolution, exchange semantics, full command schema).
- `docs/teams.md`: the custom-team guide (role anatomy, per-role model
  selection as cost control, briefs, credential isolation, a worked
  pipeline example).

### Changed
- Deck renders and status-card tails are cached per pane behind a
  screen-change sequence (`Pane.Seq()`): idle panes cost nothing per frame
  instead of a full grid walk on every message.
- Scrollback replay is cached per (sequence, width): scrolling re-windows
  the parsed history for free instead of re-parsing up to 256KB of ANSI per
  frame.
- The pane history ring is a true circular buffer: one allocation per pane
  at start instead of steady GC churn on every write once full.

### Fixed
- Pane input lifecycle no longer leaks goroutines: a full inbox drops input
  with a typed error instead of spawning blocked goroutines, and closed
  panes release their writer (accumulated across role restarts).
- IPC exchanges carry deadlines on both sides, so a silent client cannot
  park deck goroutines and a wedged deck cannot hang the
  `delegate`/`work-done` CLI.
- An exact-boundary fill of the new circular buffer no longer reports an
  empty history.
- CI resolves the newest Go patch release (`check-latest`), so stdlib
  security fixes land without manual workflow bumps.

## [0.3.0] - 2026-07-03

### Added
- `choragos init --template <name>`: embedded team templates (`starter`,
  `claude-team`, `mixed-team`, `review`).
- Live token and cost display on the sidebar cards, fed by the gateway's
  `sphragis_tokens_total` metrics (sphragis >= 0.8): per-role
  `ANTHROPIC_BASE_URL` carries an `/agent/<role>` suffix for attribution,
  and an optional `[pricing]` table (USD per million tokens, longest
  model-prefix match) turns tokens into cost.

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

[Unreleased]: https://github.com/sphragis-oss/choragos/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/sphragis-oss/choragos/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/sphragis-oss/choragos/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/sphragis-oss/choragos/compare/v0.4.2...v0.5.0
[0.4.2]: https://github.com/sphragis-oss/choragos/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/sphragis-oss/choragos/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/sphragis-oss/choragos/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/sphragis-oss/choragos/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/sphragis-oss/choragos/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sphragis-oss/choragos/releases/tag/v0.1.0
