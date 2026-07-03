# Long-running sessions (detach/attach)

Choragos is a single in-process TUI: it does not ship its own server/client
detach. For sessions that must survive a closed laptop lid or an SSH drop,
run the deck inside a terminal multiplexer and let it own detach/attach.

## tmux

```bash
tmux new-session -s agents 'choragos serve'
# detach: ctrl+b d (tmux prefix, outside choragos)
tmux attach -t agents
```

Two things to keep in mind:

- **Prefix collision.** tmux and choragos both default to `ctrl+b`. Inside
  tmux you must press `ctrl+b ctrl+b` to reach choragos, or rebind one side.
  The cleanest option is a different choragos prefix in `.choragos.toml`:

  ```toml
  [keys]
  prefix = "ctrl+s"
  ```

- **Resize on attach.** Reattaching from a different terminal size sends a
  resize through tmux; choragos re-tiles and calls `pane.Resize` on every
  visible tile, so the agents reflow on their own.

## zellij

```bash
zellij --session agents
# inside: choragos serve
zellij attach agents
```

zellij's default keymap is modal (`ctrl+p`, `ctrl+t`, ...) and does not
collide with `ctrl+b`.

## What not to do

Do not run one agent per tmux pane and script `send-keys` orchestration
around it. Choragos exists precisely because polling a multiplexer's screen
for agent readiness is racy; keep the agents inside choragos's owned PTYs
and put the multiplexer around the whole deck.
