# Long-running sessions (detach/attach)

Since v0.7 the deck has native detach: the session (agents, PTYs, task
board, gates, scrollback) runs as a headless server per working
directory, and the TUI is a client you can attach and detach at will. A
closed laptop lid or a dropped SSH connection kills the client, never
the crew.

## The lifecycle

```bash
choragos serve --detach        # start the session headless, return immediately
choragos attach                # bring up the TUI (run it from the same directory)
# prefix+d                     # detach: the TUI exits, the session keeps running
choragos attach                # come back later: screens, tasks, gates, layout restored
choragos ls                    # list running sessions (any directory)
choragos kill [--all]          # stop this directory's session (agents included)
```

`choragos serve` without `--detach` is unchanged: one foreground process,
quit with `ctrl+q`. From an attached client `ctrl+q` also stops the whole
session; `prefix+d` is the one that leaves it running.

What survives a detach:

- The agents and their PTYs (they never notice the client leaving).
- Scrollback: on attach the client replays each pane's history ring
  (256 KiB per role, the same bound scrollback always had).
- The task board, pending approval gates, and restart budgets.
- Your tiling layout: it is checkpointed to the server on every window
  action and restored on the next attach.

While nobody is attached, delegations still flow (workers talk to the
control socket, not the TUI), gates queue up, `restart = "on-failure"`
still supervises, and `[ui] on_gate` / `on_input` hooks still fire, so
you hear about a waiting gate even with no client. `events.log` records
everything in between.

## Resume after quit

Detach keeps the server alive; resume brings a session back after it
fully stopped. The deck writes its state (task board, task-id counter,
pending gates, roster order with tombstones, tiling layout) to
`.choragos/session.json` on every board or gate change and on quit, and

```bash
choragos serve --resume            # also works with --detach
```

restores it: roles respawn, the board and gates come back exactly as
they were, and the orchestrator's boot context carries the usual
mid-session recap (in-flight ids, pending gate count, completed count)
built from the restored board.

What resume is not: the agents' own LLM context. Their processes died
with the deck; they come back fresh, informed by the recap. Agents with
a native resume flag can add it to their `args` (for example
`--continue`), exactly as after a model swap on reload.

In-flight tasks restore as in flight. The worker behind them is gone,
so no `work-done` will arrive; the orchestrator sees them in the recap
and decides, with the delegation timeout as the backstop.

Resume refuses loudly instead of guessing: a corrupt or
version-mismatched snapshot, a different `--config` path, or a config
that dropped a live role from the snapshot all abort with the reason.
Roles the config *added* since the snapshot spawn normally, appended
after the restored roster. Without `--resume` a leftover snapshot is
ignored and overwritten as the new session progresses.

## Handoff to the next session

Resume brings the *same* session back; handoff is a deliberate session
boundary that carries the context to a *next* one, optionally with a
different team. Each session keeps one immutable orchestrator (the
"orchestrator always exists" rule); the sessions succeed each other.

```bash
choragos handoff                          # keep the same team config
choragos handoff --config new-team.toml   # hand off to a new team
# or prefix+H in the deck (confirm overlay)
```

The deck asks the running orchestrator to write
`.choragos/handoff-session.md` (goal, state of the work, in-flight and
remaining tasks, anything the next team must know), then quits once the
file is written, or after 2 minutes without it: the document is an
enrichment, not a dependency. You can also write or edit it by hand
before resuming. The orchestrator is asked to append a dated entry to
`.choragos/memory.md` first, so the session's lessons outlive the
handoff (see Project memory below).

An attached client is told the end is deliberate: it exits cleanly
with `session ended: handoff complete` (and the resume hint) instead
of reporting a lost connection; `choragos kill` says goodbye the same
way.

The next `choragos serve --resume` (with `--config new-team.toml` when
one was named) restores the board as usual and attaches the handoff
document to the new orchestrator's boot context after the recap. Roles
the new config drops become tombstones instead of refusing the resume,
so the board history and its indices stay intact; the stored layout is
deliberately not carried across a handoff.

## Project memory

Handoff carries state to the *next* session; `.choragos/memory.md`
carries knowledge to *every* session. When the file exists and is
non-empty, each orchestrator's boot context points at it, so decisions,
gotchas, and conventions ("the tests need `-tags integration`", "we
decided against go-git in #69") are not relearned run after run.

The file is yours: plain markdown, hand-written, hand-edited, and
committable if the whole team should share it. A dated heading per
entry keeps it scannable:

```markdown
## 2026-08-13
- integration tests need -tags integration
- the flaky suite is internal/wm; retry before digging
```

Delete the file (or empty it) to boot stateless. `choragos handoff`
also feeds it automatically: the orchestrator is asked to append an
entry before writing the handoff document, so a deliberate session end
leaves its lessons behind.

## Sessions are per directory

One session per working directory: sockets and metadata live under a
runtime dir keyed by a hash of the project path, so different projects
never collide and `choragos delegate`/`attach`/`kill` find the right
session by being run from the project directory. Workers spawned by the
deck inherit `CHORAGOS_SOCK` and are unaffected.

Only one client can be attached at a time; a second `choragos attach`
is refused with the holder's pid.

## Version skew

`brew upgrade` while a session runs means the new client refuses the old
server with a clear message. Finish or `choragos kill` the session and
start a new one; there is no live migration.

## tmux / zellij still work

Wrapping the foreground deck in a multiplexer remains a fine option,
e.g. when you want the whole terminal (not just the deck) to survive.
Mind the prefix collision: tmux and choragos both default to `ctrl+b`,
so rebind one side (`[keys] prefix = "ctrl+s"`).

## What not to do

Do not run one agent per tmux pane and script `send-keys` orchestration
around it. Choragos exists precisely because polling a multiplexer's
screen for agent readiness is racy; keep the agents inside choragos's
owned PTYs.
