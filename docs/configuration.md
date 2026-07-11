# Configuration reference

Choragos loads `.choragos.toml` from the working directory (override with
`choragos serve --config <path>`). Without a config it runs the built-in
5-role team. Generate a commented starter with `choragos init`, pick a team
template with `choragos init --template <starter|claude-team|mixed-team|review>`,
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
broadcast = "a"
task_board = "t"
search = "/"
help = "?"
```

See [keybindings.md](keybindings.md) for what each action does.

## `[ui]`

| Key | Type | Default | Meaning |
|-----|------|---------|---------|
| `auto_focus` | bool | `true` | Focus the tile of whichever agent produces output or receives a delegation; any manual focus action pauses it |
| `sidebar` | bool | `true` | Start with the status-card sidebar visible |
| `bell` | bool | `true` | Ring the terminal bell when an agent transitions to waiting-for-input |
| `mouse` | bool | `true` | Capture the mouse for tile focus and wheel scrollback; set `false` to restore terminal-native text selection (no Shift+drag needed) |
