# Troubleshooting

Run `choragos doctor` first: it checks the config (including unknown-key
typos), role binaries on PATH, the control socket, `TERM`, and the sphragis
gateway, and prints one OK/WARN/FAIL line per check.

## The deck starts but a role shows "exited" immediately

The role's command failed to start or crashed. Check
`.choragos/logs/<role>.log` for the process output. Shell aliases do not
resolve; set `command` to the real binary. Restart the role in place with
`prefix+R`.

## `delegate failed (is the deck running?)`

The CLI resolves the socket via `$CHORAGOS_SOCK`, then `$XDG_RUNTIME_DIR`,
then the temp dir. It must resolve to the same path the deck bound. Inside
role panes choragos exports `CHORAGOS_SOCK` automatically; from another
shell, export it yourself.

## `bind: invalid argument` on startup

Unix socket paths cap at ~104 characters. Set `CHORAGOS_SOCK` to a short
path such as `/tmp/choragos.sock`.

## Delegation is refused / "dispatch refused: gateway down"

Sphragis is enabled and fail-closed (the default) but the gateway is not
healthy. `ctrl+g` toggles enforcement live, `--sphragis=false` disables it
for a run, `[sphragis] fail_closed = false` keeps routing best-effort.

## ctrl+b does nothing / goes to the wrong program

Inside tmux the default prefixes collide: press `ctrl+b ctrl+b`, or change
one side. See [long-running-sessions.md](long-running-sessions.md).

## I cannot select text with the mouse

The deck captures the mouse for click-to-focus and wheel scrollback. Use
your terminal's override, usually Shift+drag (Alt+drag in some terminals),
to select text.

## A role's status is wrong (stuck on "working" or missing "waiting")

Status is heuristic, based on the agent TUI's visible screen. Extend the
markers per role with `input_prompts` (blocking prompts) and
`chrome_markers` (statusline noise); see
[configuration.md](configuration.md).

## The terminal is garbled after a crash

The deck restores the terminal and SIGTERMs agents even on panic, and
writes the stack to `.choragos/logs/crash.log`; please attach that file to
a bug report. If a hard kill (SIGKILL) left the terminal raw, run `reset`.
