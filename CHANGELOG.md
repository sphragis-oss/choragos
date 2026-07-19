# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.13.0] - 2026-07-19

Team evolution, cost control, and clean context per task.

### Added
- Fresh workers: `fresh = true` respawns a role before every delegation
  (judge retry rounds included), so each task starts with clean agent
  context instead of accumulating every earlier ticket. Tasks travel as
  files, so nothing is lost across the respawn. (#153, #154)
- Orchestrator handoff for fresh roles: the orchestrator is asked to
  keep a short per-role handoff in `.choragos/handoff-<role>.md`
  (decisions, files touched, gotchas), and the deck attaches it to a
  fresh role's next task automatically when it exists. (#163)
- Team evolution: on `choragos reload` the orchestrator itself can swap
  command or model, respawning with a deck-built session recap so
  nothing is lost, and the orchestrator can propose new roles with
  `choragos roster add`, applied to the config file after a human gate.
  Design note in `docs/design-team-evolution.md`. (#141, #146, #147,
  #148)
- Per-role cost budgets: `budget = "5.00"` plus `budget_action`
  (`notify` or `pause`) cap a role's session spend; needs the gateway
  metrics and a `[pricing]` table for the cost signal. `choragos
  report` gained a COST column and `--json` for machine-readable
  run summaries. (#142, #143, #150, #152)
- Per-role `base_url_env` routes a role's gateway URL into any env
  var name(s), so non-Anthropic CLIs (`OPENAI_BASE_URL`, ...) work
  behind the gateway. (#140, #145)
- Per-role `model_flag` renames or drops the `--model` argument for
  commands that do not accept it; `choragos doctor` warns when a model
  is set for a command not known to take one. (#149, #156)
- `docs/sandboxing.md`: wrapper-script recipes for running workers
  inside Docker, bubblewrap, or macOS sandbox-exec, and SECURITY.md
  now states the threat model explicitly. (#138, #139, #157)

### Changed
- `.choragos/logs` is now 0700 and everything in it (role transcripts,
  `events.log`, crash and server logs) 0600: transcripts can contain
  whatever agents print, including secrets. Existing log directories
  are tightened on the next run. (#158)
- CI/supply chain hardened: harden-runner egress moved from audit to
  block with per-job allowlists, gosec and actionlint jobs, wire
  protocol fuzz tests, OpenSSF Scorecard badge, and patch coverage is
  now a required check. (#133, #134, #136, #159, #160, #161)

### Fixed
- Every `choragos` command leaked an OSC 11 terminal reply
  (`^[]11;rgb:...`) into the shell after `serve --detach` and stalled
  up to ~5s on non-answering terminals: bubbletea v1 queries the
  terminal background at init in every linking binary. The dependency
  now points at a fork with the init-time query removed; a regression
  test asserts no query bytes are emitted. (#132, #155)
- Choragos Desktop now shows delegation timeouts on the task board
  (the timeout mark previously never reached the wire protocol) and
  "over budget" on role cards, matching the TUI. This release pairs
  with `desktop/v0.13.0`. (#164)

## [0.12.0] - 2026-07-18

### Added
- Autonomous judge loop, the machine gate beside the human `approve`
  gate: set `judge = "<role>"` (plus `judge_pass`, `judge_rounds`) on
  a role and the deck scores each delegation through the judge role
  and retries with the critique until the score passes or the round
  cap runs out. The verdict is a strict `VERDICT: <n>/10` first line
  in the judge's `work-done --report` file; any ambiguity (invalid
  verdict, judge timeout or exit, exhausted cap) fails closed into
  the human gate queue with a reason. Rounds and scores show on the
  task board in the TUI and the desktop app, `judge` events land in
  `events.log`, and `choragos doctor` warns when a role and its judge
  run the same command and model. Design note in
  `docs/design-judge-loop.md`. (#129, #130)
- Choragos Desktop: native macOS notifications when the window is in
  the background and a delegation awaits approval or an agent blocks
  waiting for input. (#125)

### Changed
- Role transcripts' contract is now documented: they record
  PTY-visible output, so full-screen agent TUIs (claude-code among
  them) leave only their last screen by design; audit those roles
  from `events.log`, `choragos report`, and the delegation report
  files. Closes the transcript-size investigation. (#91, #128)
- `docs/configuration.md` gained Slack, Discord, and ntfy recipes for
  the existing `on_gate`/`on_input`/`on_timeout` hooks. (#126)

## [0.11.2] - 2026-07-17

### Added
- Choragos Desktop, a native macOS app: attach to running sessions or
  start one from a project folder, live terminal panes, approval
  gates, task board, restart/pause per role. Ships separately as
  `desktop/vX.Y.Z` tags with the version-matched CLI bundled inside
  the `.app`; this release pairs with `desktop/v0.11.2`.
  (#113, #114, #115, #116, #117, #120, #121, #122)

### Changed
- The attach wire protocol client moved from `internal/deck` to
  `internal/wire` (`Dial`/`Replay`/`Pump`, typed busy and mismatch
  errors) so any client, TUI or GUI, speaks the same protocol.
  Behavior-neutral for the CLI. (#111)

### Fixed
- `choragos attach` only says "no session is running here" for a
  missing or dead socket (ENOENT/ECONNREFUSED); other mid-handshake
  failures now surface as themselves instead of hiding behind that
  hint. (#112)

## [0.11.1] - 2026-07-16

### Fixed
- The mouse wheel scrolls the tile under the cursor, focusing it first
  when needed, instead of always driving the focused pane's history.
  Over the sidebar or the status row it keeps scrolling the focused
  tile. (#106)

## [0.11.0] - 2026-07-15

Run visibility and runtime control, plus a standalone-first gateway
posture: choragos now works out of the box with or without Sphragis.

### Added
- `choragos report`: aggregate `.choragos/logs/events.log` (or any
  saved copy) into a per-role table of tasks handled, completions,
  busy and average task time, first/last activity, and token usage.
  With the gateway on, cumulative per-role token counters are
  snapshotted into the event log every 30s and on quit, so token burn
  survives the session; without it the column reads n/a. (#86)
- Wall-clock timeout per delegation: `timeout = "45m"` on a role arms
  a timer from delivery to work-done. The default action notifies
  (bell, board `timeout` mark, `[ui] on_timeout` hook) and keeps the
  worker running; `timeout_action = "restart"` SIGTERMs the role so
  `restart = "on-failure"` respawns it. (#100)
- Pause/resume a role with `prefix+p`: SIGSTOP/SIGCONT on the process
  group, a `paused` status in the deck and over detach/attach, no
  false waiting bell, and paused time never counts toward delegation
  timeouts. Freeze a worker, inspect the diff, resume with its
  context window intact. (#101)
- Role transcripts stream: lines are appended to
  `.choragos/logs/<role>.log` as they scroll off the live screen
  (every 15s) instead of only at pane close, with a
  `--- transcript gap ---` marker when output outran the capture
  buffer. Long sessions survive on disk, including after a crash.
  Note: agents that redraw in place without emitting scrollback
  (claude-code) still leave only what the terminal ever showed. (#91)
- Each role's sidebar card shows its model: the dominant model from
  gateway traffic when available (`claude-opus-4-8` renders as
  "Claude Opus 4.8"), else the configured `model`, else nothing. (#94)
- The terminal window title carries the workspace
  (`choragos · <dir>`), so parallel decks are distinguishable. (#95)

### Changed
- The gateway default is now soft: with `[sphragis] enabled` unset,
  no `--sphragis` flag, the binary missing from PATH, and nothing
  listening on the gateway address, the deck starts with the gateway
  off and a clear warning instead of failing closed. Explicit
  `enabled = true` keeps the strict spawn-or-fail-closed behavior,
  and `choragos doctor` distinguishes the two cases. (#92)

### Fixed
- Unmapped keys are no longer typed into panes as their names:
  control chords forward their control bytes (ctrl+a etc.),
  home/end/delete/pgup/pgdn send proper sequences, and anything else
  is dropped. (#98)

## [0.10.0] - 2026-07-15

First-user UX batch, driven by live feedback from a team demo.

### Added
- The focused tile renders the child's terminal cursor as a
  reverse-video block at the live tail, so you can see where your
  input lands. Apps that hide their cursor stay clean; unfocused
  tiles and scrolled-back views never show one. (#81)
- Scrollback sense of place: while scrolled back, the status line
  shows the position and the way back
  (`scrollback ↑15/180 · PgDn live · ctrl+b / search`) and the
  focused tile's right border carries a proportional scrollbar
  thumb. Both disappear at the live tail. (#82)
- Open briefs and reports in your own editor: `e` on a task-board
  entry opens it in `$VISUAL`/`$EDITOR` (like the gate overlay's
  `e`), and `[ui] viewer = "editor"` makes `v` open the editor
  everywhere, with the in-app pager as the fallback when no editor
  is set. (#84)
- Direct role focus: `prefix+1..9` jumps to the role by its sidebar
  card number, and a left click on a sidebar card does the same.
  Hidden roles surface on the focused tile; no more cycling
  `ctrl+o` around the team. (#79, #80)

## [0.9.0] - 2026-07-14

### Added
- Workspace checkpoints: in a git repository, every delegation
  snapshots tracked and untracked files (gitignore respected,
  choragos's own `.choragos/` state excluded) as a parentless commit
  under `refs/choragos/checkpoints/<epoch>-<task-id>`, taken before
  the task reaches the worker. The user's index, HEAD, and history
  are never touched; a snapshot failure warns and never blocks the
  delegation. New `[checkpoints]` config: `enabled` (default true)
  and `keep` (default 20, pruned at session start). `choragos doctor`
  reports whether snapshots are active.
- Rollback, from the deck and the CLI: `u` on a task board entry
  opens a confirm overlay with the checkpoint's age and the exact
  restore/delete counts; `choragos rollback <task-id>` does the same
  with no session running, and `choragos checkpoints` lists what you
  can go back to. Rollback restores files only (HEAD, branches, the
  index, the stash, and worker commits stay untouched), never touches
  ignored files, and checkpoints the current state first, so every
  rollback is undoable (`choragos rollback pre-rollback-<task-id>`).

### Fixed
- Two flaky deck tests (TestScrollbackSearch, TestServerAttachLifecycle)
  that could fail CI on slow runners; test-only changes.

## [0.8.0] - 2026-07-13

### Added
- In-app pager for briefs and reports: `v` on the approval overlay pages
  the gated delegation's brief, and on the task board `j`/`k` select a
  task and `v` pages its brief or report, all without leaving the deck.
  Markdown renders styled (glamour, auto light/dark), anything else
  falls back to plain text, files are capped at 512 KB, and
  `esc`/`q` returns to the overlay it came from with the gate intact.
- `choragos init --auto`: detects the project from its manifests
  (`go.mod`, `package.json`, `Cargo.toml`,
  `pyproject.toml`/`setup.py`/`requirements.txt`) and writes a team
  whose coder and reviewer briefs are tailored to that language
  (gofmt/go test, eslint/npm test, clippy/cargo test, ruff/pytest).
  In a multi-language repo the dominant language by source count wins
  and the others are noted in a comment; with no manifest it falls
  back to the starter template. Detection is static file inspection:
  no network, no LLM calls.

### Security
- goldmark bumped to v1.8.4, past the GO-2026-5320 XSS fix (pulled in
  as a dependency of the new markdown pager).

## [0.7.5] - 2026-07-13

### Added
- `[ui.theme]`: the deck's status colors are configurable (accent,
  working, waiting, scroll, idle, dim) as ANSI 0-255 palette indices
  or `#rrggbb` hex, so the deck matches your terminal. Omitted keys
  keep the classic palette; an invalid value warns at startup (and in
  events.log) and keeps the default. Foreground serve, headless
  server, and attached clients all render from the config the session
  was started with.

## [0.7.1] - 2026-07-13

### Fixed
- Respawning a role while a client is attached (auto-restart, `prefix+R`,
  or a reload spec change) now resets that pane on the client: the fresh
  boot output starts on a clean screen instead of appending under the
  old content. The same change fixes a real suppression bug: the
  respawned pane's ring sequence restarts from zero, so the attach-time
  snapshot sequence could silently filter out all post-respawn output
  for that role; the sequence now restarts with the pane, and chunks
  still queued from the replaced pane are dropped by identity.

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

[Unreleased]: https://github.com/sphragis-oss/choragos/compare/v0.13.0...HEAD
[0.13.0]: https://github.com/sphragis-oss/choragos/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/sphragis-oss/choragos/compare/v0.11.2...v0.12.0
[0.11.2]: https://github.com/sphragis-oss/choragos/compare/v0.11.1...v0.11.2
[0.11.1]: https://github.com/sphragis-oss/choragos/compare/v0.11.0...v0.11.1
[0.11.0]: https://github.com/sphragis-oss/choragos/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/sphragis-oss/choragos/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/sphragis-oss/choragos/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/sphragis-oss/choragos/compare/v0.7.5...v0.8.0
[0.7.5]: https://github.com/sphragis-oss/choragos/compare/v0.7.1...v0.7.5
[0.7.1]: https://github.com/sphragis-oss/choragos/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/sphragis-oss/choragos/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/sphragis-oss/choragos/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/sphragis-oss/choragos/compare/v0.4.2...v0.5.0
[0.4.2]: https://github.com/sphragis-oss/choragos/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/sphragis-oss/choragos/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/sphragis-oss/choragos/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/sphragis-oss/choragos/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/sphragis-oss/choragos/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sphragis-oss/choragos/releases/tag/v0.1.0
