# Keybindings

The deck has two kinds of bindings: direct chords that always work, and
prefix-mode window-manager actions. In normal mode every other key is forwarded
verbatim to the focused pane's PTY.

## Direct chords (always active)

| Key | Action |
|-----|--------|
| `ctrl+q` | Quit: SIGTERM every agent, wait for graceful exit, then close |
| `ctrl+g` | Toggle sphragis gateway enforcement |
| `ctrl+o` | Cycle focus to the next role (retargets the focused tile when the role is hidden) |
| `PgUp` / `PgDn` | Scroll the focused tile's scrollback |

## Prefix mode (window manager)

Press the prefix (default `ctrl+b`) to arm WM mode; the status line shows
`[PREFIX]`. The next key runs its action and returns to normal mode. An
unmapped key is a no-op that exits prefix mode. The prefix byte is never
forwarded to the PTY.

| Action | Default | Behavior |
|--------|---------|----------|
| `split_vertical` | `prefix+v` | Split the focused tile left/right; the new tile shows the next hidden role |
| `split_horizontal` | `prefix+-` | Split the focused tile top/bottom, same fill rule |
| `close_pane` | `prefix+x` | Remove the focused tile; the agent keeps running and its sidebar card remains |
| `focus_pane_left` | `prefix+h` | Focus the tile to the left |
| `focus_pane_down` | `prefix+j` | Focus the tile below |
| `focus_pane_up` | `prefix+k` | Focus the tile above |
| `focus_pane_right` | `prefix+l` | Focus the tile to the right |
| `cycle_pane_next` | `prefix+tab` | Focus the next visible tile in tree order |
| `cycle_pane_previous` | `prefix+shift+tab` | Focus the previous visible tile |
| `zoom` | `prefix+z` | Fullscreen the focused tile; toggle again to restore the exact layout |
| `resize_mode` | `prefix+r` | Enter resize mode (status line shows `[RESIZE]`) |
| `toggle_sidebar` | `prefix+b` | Show/hide the status-card sidebar; tiles reflow to the full width |
| `restart_role` | `prefix+R` | Respawn the focused tile's agent (works on live or exited roles) |
| `broadcast` | `prefix+a` | Toggle sending normal-mode keys to every live pane (`[BCAST]`) |
| `task_board` | `prefix+t` | Overlay of delegations with pending/done status and durations; any key closes |
| `search` | `prefix+/` | Search the focused tile's scrollback; Enter jumps, `n`/`N` navigate |
| `help` | `prefix+?` | Keymap overlay; any key closes |

Splitting never spawns a new process: roles are fixed from config, and tiles
only arrange which of the role panes are visible. When every role already has
a tile, split is a no-op.

## Resize mode

Inside resize mode, `h`/`l` (or left/right arrows) move the focused vertical
divider and `j`/`k` (or down/up arrows) move the focused horizontal divider,
resizing the panes live. Any other key exits resize mode.

## Configuration

Bindings live under `[keys]` in `.choragos.toml`; values are bubbletea key
names (`ctrl+a`, `v`, `-`, `tab`, `shift+tab`, ...). A `prefix+` prefix and
`minus` are accepted for herdr-style values. Omitted keys keep their default.

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

[ui]
auto_focus = true # focus the agent that gets a delegation or blocks on input
sidebar = true    # start with the status-card sidebar visible
bell = true       # terminal bell when an agent blocks on input
```

## Mouse

Cell-motion mouse mode is on: a left click focuses the tile under the
cursor, and the wheel scrolls the focused tile's history. The sidebar and
status row are not clickable.

## Scrollback search

`prefix+/` opens a query prompt on the status line (`[SEARCH /...]`).
Enter jumps to the nearest match above the current view; while scrolled
back, `n` steps to older matches and `N` back to newer ones. Esc cancels.
PgDn to the live tail releases `n`/`N` back to the agent.

## Per-role status heuristics

The waiting-for-input and chrome detection heuristics can be extended per
role for non-Claude agent TUIs:

```toml
[[roles]]
name = "reviewer"
command = "agy"
input_prompts = ["continue? <enter>"]
chrome_markers = ["agy statusbar"]
```

With `auto_focus = true` (the default) the deck focuses the agent that
receives a delegation, reports back, or blocks waiting for input,
retargeting the focused tile when that role is hidden. Raw output never
steals focus. Set it to `false` for a fully user-controlled layout. Any
manual focus action (`ctrl+o` or a WM action) pauses auto-focus for the
session.

On terminals too small to tile, the deck degrades to the focused tile only,
dropping borders when even that does not fit.
