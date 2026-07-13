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
