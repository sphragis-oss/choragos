# Configuration reference

Choragos loads `.choragos.toml` from the working directory (override with
`choragos serve --config <path>`). Without a config it runs the built-in
5-role team. Generate a commented starter with `choragos init`, pick a team
template with `choragos init --template <starter|claude-team|mixed-team|review>`
(or `choragos init --auto` to detect the project and get language-specific roles),
and check a setup with `choragos doctor`.

Unknown keys are reported as warnings on startup (and in
`.choragos/logs/events.log`), so typos never fail silently.

## `[[roles]]`

One table per agent seat. Roles are fixed for the lifetime of the deck.

| Key | Type | Default | Meaning |
|-----|------|---------|---------|
| `name` | string | required | Unique role name; targeted by `choragos delegate --to <name>` |
| `command` | string | required | Agent binary, resolved on PATH (shell aliases do not resolve) |
| `args` | array | `[]` | Extra argv passed to the command |
| `model` | string | `""` | Appended as `--model <value>` when set |
| `prompt_template` | string | `""` | Role brief injected at boot and prefixed to delegated tasks |
| `start` | bool | `false` | Marks the orchestrator: receives the delegation protocol and `work-done` reports; exactly one role should set it |
| `input_prompts` | array | `[]` | Extra markers (substring, case-insensitive) that mean "blocked waiting for input", added to the built-ins |
| `chrome_markers` | array | `[]` | Extra markers for TUI chrome lines to drop from the sidebar activity preview |
| `env_allow` | array | `[]` | Switch the role to an env allowlist: baseline vars (`PATH`, `HOME`, `TERM`, `SHELL`, `LANG`, `LC_*`, `XDG_*`, ...) plus these names or `PREFIX_*` patterns |
| `env_deny` | array | `[]` | Strip matching vars (exact or `PREFIX_*`) in either mode; wins over `env_allow` |
| `restart` | string | `""` | `"on-failure"` respawns the role in place when its process exits non-zero (or dies by signal); clean exits and deck shutdown are respected |
| `restart_retries` | int | `3` | Auto-restart budget per role, so a broken command cannot crash-loop; a manual `prefix+R` resets it |
| `timeout` | string | `""` | Wall-clock limit per delegation to this role (Go duration, e.g. `"45m"`); empty disables. The timer starts when the task is delivered (after any approval gate) and clears on the matching work-done |
| `timeout_action` | string | `"notify"` | What a timeout does: `notify` (bell, board `timeout` mark, `on_timeout` hook; the worker keeps running) or `restart` (SIGTERM the role; auto-restart takes over with `restart = "on-failure"`) |
| `approve` | bool | `false` | Human gate: delegations to this role pause in the deck until the user approves (`y`) or rejects (`n`); `v` pages the attached brief in-app, `e` opens it in `$VISUAL`/`$EDITOR`, and a rejection is reported back to the orchestrator |

Environment isolation example: a reviewer that never sees cloud credentials.

```toml
[[roles]]
name = "reviewer"
command = "claude"
model = "sonnet"
env_deny = ["AWS_*", "GITHUB_TOKEN"]
```

`CHORAGOS_SOCK` (control socket) and, when the gateway is enabled,
`ANTHROPIC_BASE_URL` are always injected by choragos itself.

## `[sphragis]`

| Key | Type | Default | Meaning |
|-----|------|---------|---------|
| `enabled` | bool | `true` | Route agent LLM traffic through the Sphragis gateway |
| `addr` | string | `127.0.0.1:8787` | Gateway listen address |
| `command` | string | `sphragis` | Gateway binary to start or attach to |
| `fail_closed` | bool | `true` | Refuse delegation while the gateway is down |

`ctrl+g` toggles enforcement live; `--sphragis=false` disables it for a run.

The `enabled` default is soft: when you never set it (no config key, no
`--sphragis` flag) and the `command` binary is not in PATH with nothing
listening on `addr`, the deck starts with the gateway off and logs a
warning instead of failing closed. Set `enabled = true` explicitly to
require the gateway and keep the fail-closed guarantee.

When the gateway is on, each role's `ANTHROPIC_BASE_URL` carries an
`/agent/<role>` suffix so the gateway attributes token usage per role
(sphragis >= 0.8). The sidebar cards then show live token counts, and cost
when `[pricing]` is set. Counters reset when the gateway restarts.

## `[pricing]`

Optional. One table per model-name prefix (longest prefix wins), in USD per
million tokens; directions you omit cost 0. With no `[pricing]` the cards
show token counts only.

```toml
[pricing."claude-sonnet-5"]
input = 3.0
output = 15.0
cache_read = 0.3
cache_creation = 3.75
```

## `[keys]`

The prefix chord plus one key per action. Values are bubbletea key names
(`ctrl+a`, `v`, `-`, `tab`, `shift+tab`); a `prefix+` prefix and `minus` are
accepted for herdr-style values. Single-character bindings are
case-sensitive (`R` means shift+r). Defaults:

```toml
[keys]
prefix = "ctrl+b"
split_vertical = "v"
split_horizontal = "-"
close_pane = "x"
focus_pane_left = "h"
focus_pane_down = "j"
focus_pane_up = "k"
focus_pane_right = "l"
cycle_pane_next = "tab"
cycle_pane_previous = "shift+tab"
zoom = "z"
resize_mode = "r"
toggle_sidebar = "b"
restart_role = "R"
reload = "C"
detach = "d"
broadcast = "a"
task_board = "t"
search = "/"
help = "?"
```

See [keybindings.md](keybindings.md) for what each action does.

## `[ui]`

| Key | Type | Default | Meaning |
|-----|------|---------|---------|
| `auto_focus` | bool | `true` | Focus the agent that receives a delegation, reports back, or blocks waiting for input (never on raw output); any manual focus action pauses it |
| `sidebar` | bool | `true` | Start with the status-card sidebar visible |
| `bell` | bool | `true` | Ring the terminal bell when an agent transitions to waiting-for-input |
| `mouse` | bool | `true` | Capture the mouse for tile focus and wheel scrollback; set `false` to restore terminal-native text selection (no Shift+drag needed) |
| `on_gate` | string | `""` | Command run via `sh -c` (background, non-blocking) when a delegation joins the approval queue; `CHORAGOS_ROLE` and `CHORAGOS_TASK` are in its env |
| `on_input` | string | `""` | Command run via `sh -c` (background, non-blocking) when an agent transitions to waiting-for-input; `CHORAGOS_ROLE` is in its env |
| `on_timeout` | string | `""` | Command run via `sh -c` (background, non-blocking) when a delegation outlives its role's `timeout`; `CHORAGOS_ROLE` and `CHORAGOS_TASK` are in its env |
| `viewer` | string | `"pager"` | How `v` opens briefs/reports: `"pager"` renders in-app, `"editor"` opens `$VISUAL`/`$EDITOR` (pager when both are unset). The board and gate `e` always open the editor |

### `[ui.theme]`

Overrides the deck's status colors so it matches your terminal. Values
are ANSI 0-255 palette indices or `#rrggbb` hex; keys you omit keep the
default, and an invalid value warns at startup and keeps the default.

| Key | Default | Colors |
|-----|---------|--------|
| `accent` | `45` (cyan) | Focused tile border, overlay titles, prefix indicator |
| `working` | `42` (green) | Working status dot, gateway-up label, done tasks |
| `waiting` | `214` (orange) | Waiting-for-input status, approval overlay, resize/broadcast indicators |
| `scroll` | `213` (magenta) | Scrollback and search indicators |
| `idle` | `244` (grey) | Idle status dot |
| `dim` | `240` (dark grey) | Unfocused tile borders, exited roles, gateway-off label |

```toml
[ui.theme]
accent = "#7aa2f7"
waiting = "179"
```

Notification hooks reach you when the terminal bell cannot (another
window, another room). A failing hook is logged to `events.log` and
otherwise ignored. macOS example with
[terminal-notifier](https://github.com/julienXX/terminal-notifier):

```toml
[ui]
on_gate  = "terminal-notifier -title choragos -message \"$CHORAGOS_ROLE: delegation awaiting approval\""
on_input = "terminal-notifier -title choragos -message \"$CHORAGOS_ROLE is waiting for input\""
```

## `[checkpoints]`

Per-delegation workspace snapshots (git repositories only; see
[design-checkpoints.md](design-checkpoints.md)). Every delegation
snapshots tracked and untracked files (gitignore respected) as a
parentless commit under `refs/choragos/checkpoints/<epoch>-<task-id>`,
before the task reaches the worker. The user's index, HEAD, and
history are never touched. In a non-git directory the deck warns at
startup and runs unprotected.

| Key | Type | Default | Meaning |
|-----|------|---------|---------|
| `enabled` | bool | `true` | Snapshot the workspace on every delegation |
| `keep` | int | `20` | Newest checkpoints retained; older ones are pruned at session start |

Recovery is post-hoc, from the CLI (no session needed):

```bash
choragos checkpoints              # list: task id, age, target role
choragos rollback T3              # restore files to the state before T3 ran
choragos checkpoints prune        # apply the retention policy now
```

`rollback` restores files (deleting ones the task created) but never
moves HEAD, branches, the index, the stash, or commits a worker made;
ignored files are never touched. It checkpoints the current state
first, so `choragos rollback pre-rollback-T3` undoes it.

## Reloading the config at runtime

Edit the config file, then `choragos reload` (or `prefix+C` in the deck):
the deck re-reads the file it was started with and converges the team on
it by role name. Added roles spawn and get their boot brief; removed roles
are stopped gracefully and disappear from the sidebar and delegation
targets; a changed `command`/`args`/`model`/`env_*` respawns that role
with the new spec; changed `prompt_template`/`approve`/`restart*` apply
without a restart, on the next task.

Guardrails, all reported in `events.log`:

- The start role's process is never touched: its spec changes and removal
  are ignored until a deck restart (prompt-only changes still land).
- A role with a pending approval gate or an unresolved delegation is not
  respawned; rerun the reload once its work resolves. Removing such a
  role outright is honored (that is an explicit decision).
- Running on the built-in team (no config file) there is nothing to
  re-read, so reload is refused.
- `[keys]`, `[ui]`, and `[sphragis]` changes need a deck restart; only
  `[[roles]]` converges live.
