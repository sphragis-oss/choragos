# Design: the session core, runtime roles, and detach/attach

Covers issues [#24](https://github.com/sphragis-oss/choragos/issues/24)
(add/remove roles without restarting the deck) and
[#46](https://github.com/sphragis-oss/choragos/issues/46) (daemon/attach
session resumption). They share this design pass because they break the
same two assumptions, so the wrong fix for one blocks the other.

Status: proposal. Nothing here is implemented; staging is at the end.

## The two assumptions being broken

Today the deck is one process built around one `Model`
(`internal/deck/deck.go`): Bubble Tea drives the UI, the same struct owns
the agent PTYs, the vt10x emulators and 256 KiB raw scrollback rings
(`internal/pane/pane.go`), the task board, the approval-gate queue, the
event log, the sphragis supervisor, and the IPC socket. Two assumptions
are welded in:

1. **The team is the config snapshot.** `deck.Run(cfg)` receives the
   role list once; every index-addressed structure (the `m.panes` slice,
   `frameMsg{idx, gen}` routing, the layout tree, which binds tiles to
   `int` role indices in `wm.New(role int)`) assumes the roster never
   changes size or order.
2. **The session is the terminal.** The agents are children of the TUI
   process; when the terminal dies, `prog.Run` returns and `closeAll()`
   SIGTERMs the whole crew. The only survival strategy is wrapping the
   deck in tmux/zellij (docs/long-running-sessions.md).

Issue #24 breaks assumption 1, #46 breaks assumption 2. The shared shape: the
session (roster, PTYs, buffers, tasks, gates, telemetry) must be a
long-lived core with a mutation and subscription API, and the TUI must
become a client of it.

## Non-goals (v1 of this design)

- Multiple simultaneous attached clients. One client at a time; a second
  `attach` is refused with a clear error.
- Remote attach. Unix sockets only, same host, same user, mode 0600.
- Imperative role mutation (`choragos role add ...`). The TOML file stays
  the single source of truth for the team; mutation is a config reload,
  not CLI-managed drift.
- Zero-downtime daemon upgrades. Client/server version skew is detected
  and reported, not papered over.

## Part 1: stable role identity (prerequisite, no daemon needed)

Everything role-scoped is addressed by slice index today. Removal would
shift indices and corrupt frame routing, layout binding, and focus. Two
options:

| Option | How | Cost |
|---|---|---|
| Re-key everything by role name | `map[string]*entry`, tree leaves hold names | Touches every hot path (frame routing, focus math, render loop) for no user-visible gain |
| **Tombstones (chosen)** | `m.panes` is append-only; a removed role closes its pane and is marked dead, its index never reused by reordering; new roles append | Index semantics stay valid everywhere; dead entries cost a struct each |

Tombstones win on blast radius: `frameMsg.idx`, `wm.Tree`'s `int` roles,
and the gen-guard against stale streams all keep working unchanged. The
deck already knows how to present a dead pane (`exited` state); a
tombstoned role is an exited pane whose tile is closed and which no
longer appears in delegation targets or the sidebar. A role re-added
with the same name gets a fresh entry (new index, restart budget, logs
append as they already do across restarts).

## Part 2: roles at runtime as a config reload (#24)

Declarative, not imperative: edit `.choragos.toml`, then ask the deck to
converge on it.

- **Trigger**: a new one-shot IPC verb `reload` (`choragos reload`
  CLI) plus a deck key (`prefix+C`). Both funnel into the same handler.
  The IPC channel already exists for this shape of message
  (`internal/ipc/ipc.go`, one-shot JSON commands with deadlines).
- **Diff by role name** against the running roster:
  - new name: validate (`CheckCommands`), spawn, append, boot-inject.
  - missing name: graceful stop (the `gracefulTimeout` SIGTERM path the
    deck already uses), tombstone, close its tile.
  - same name, changed `command`/`args`/`model`/`env_*`: restart in
    place through the existing `respawn` path with the new spec.
  - same name, changed `prompt_template`/`approve`/`restart*`: swap the
    stored `config.Role`; takes effect on the next task, no restart.
- **The start role is not reloadable.** Changing the orchestrator's
  command or removing it mid-session invalidates every in-flight task
  relationship; the reload rejects it with a warning and applies the
  rest. Its prompt-only changes are allowed.
- **Safety**: the reload never interrupts a role with a pending gate or
  an in-flight task silently. Roles with queued gates or unresolved
  board entries are reported ("skipped: 2 tasks in flight, rerun with
  the tasks resolved") unless the config removed them outright, which
  is treated as the user's explicit decision.
- **Orchestrator awareness**: after a successful reload the deck injects
  a one-liner into the start role ("[choragos] Team changed: +analyser,
  -release. Delegate accordingly."), because its boot prompt listed the
  old roster (`internal/prompt/prompt.go` writes the available-agents
  list at boot time only).

This part ships without any daemon. It is useful standalone (swap a
model mid-session, add a reviewer for one task) and it forces the
stable-identity work that attach also needs.

## Part 3: the session split (#46)

### Process model

One session per working directory, because the session state already
lives there (`.choragos/` context files, logs, task files). The control
socket today is per user, not per project
(`ipc.SocketPath`: `$XDG_RUNTIME_DIR/choragos.sock` or
`/tmp/choragos-<uid>.sock`), which silently serializes decks; the
session split makes the collision real, so sockets move to a per-session
path derived from the working directory:
`$XDG_RUNTIME_DIR/choragos/<8-char dir hash>.sock` (macOS `sun_path`
caps around 104 bytes, so hash, never the raw path). `CHORAGOS_SOCK`
stays as the override and stays authoritative for workers, which already
inherit it.

Lifecycle verbs:

- `choragos serve`: unchanged behavior, one process, UI attached. Under
  the hood the Model is already split (below), but nothing forks.
- `choragos serve --detach`: start the session core headless,
  double-fork, print the session id, exit.
- `choragos attach`: connect the TUI client to the running session;
  refuse (with the holder's pid) if a client is already attached.
- `prefix+d`: detach the client, leave the session running.
- `choragos ls` / `choragos kill`: enumerate and stop sessions (walk the
  socket dir, ping each).

### What lives on which side

| Session core (server) | TUI client |
|---|---|
| roster + tombstones, PTYs, reaping, restart budgets | Bubble Tea program, keymap, prefix state |
| vt10x emulators + raw rings + transcripts | its own vt10x per pane, rebuilt on attach |
| task board, gates queue, taskSeq | overlays (help, board, gate) rendered from state events |
| event log, sphragis supervisor, notification hooks | scrollback view, search, mouse |
| worker IPC (delegate/work-done) and reload | wm layout, split/zoom/resize |

The deliberate choice: **rendering stays client-side**. The server ships
raw PTY bytes; the client replays each pane's ring on attach to rebuild
the screen, exactly the mechanism the scrollback cache already uses
(`pane.go` replays the ring into a tall emulator on demand). The
alternative (server-side rendering, tmux-style frame shipping) would pull
lipgloss and the render cache into the daemon and make the wire format a
terminal protocol; shipping bytes plus JSON events keeps the server free
of UI code and the protocol dumb. Attach cost is bounded by the ring cap
(256 KiB per pane, the existing scrollback bound).

Layout is client state but is checkpointed to the server on every wm
action (an opaque blob to the server), so re-attach restores the exact
tiling. Gates and the board are server state streamed as events, so an
approval granted just before a crash is never lost with the client.

### Wire protocol (the session socket)

The existing control socket keeps its one-shot JSON contract untouched
(workers on older releases keep working). Attach uses a second socket
(`<hash>.ui.sock`), length-prefixed frames, two frame kinds:

- `output {role_idx, bytes}`: raw PTY chunks, server to client.
- `event {json}`: everything else, both directions. Client to server:
  `input`, `resize`, `gate_decision`, `wm_checkpoint`, `detach`. Server
  to client: `roster` (full, on attach, and deltas after reload),
  `board`, `gates`, `status`, `bell`, `hook`.

Handshake: client sends `hello {proto, version, cols, rows}`; server
answers `welcome {proto, version, roster, layout_blob, rings}` or
`busy {pid}` or `mismatch {server_version}`. `proto` is a single integer
bumped on any breaking change; no negotiation, matching major or refuse.

### Crash and upgrade story

- Client crash or SSH drop: the session notices the dead socket and
  keeps running; the next attach replays. This is the whole point.
- Server crash: agents die with it (they are its children). The crash
  log path stays (`writeCrashLog`), and `attach` reports "no session";
  restarting is `choragos serve` again. Agent-survives-server (double
  detach of PTY ownership) is explicitly out of scope; it would mean
  re-parenting PTYs and losing reap/exit-code semantics that supervision
  (restart on-failure) depends on.
- Upgrade: `brew upgrade` while a session runs means the new client hits
  an old server; the handshake reports the mismatch and the fix is
  "finish or kill the session, start a new one". No live migration.

## Staging

| Release | Ships | Why this order |
|---|---|---|
| v0.6 | Part 1 + Part 2: tombstones, `reload` verb + CLI + `prefix+C`, docs | Standalone value, no protocol risk, forces the identity work attach needs |
| v0.7 | Part 3: session/UI split with the in-process transport, then `--detach`/`attach`/`ls`/`kill`, per-session sockets | The split lands first behind the same single-process default (`serve` behaves identically), detach is the last mile |
| later | multi-client attach (read-only mirrors first), remote transport | Only if asked for; both are protocol extensions, not redesigns |

The v0.7 split is refactor-heavy but behavior-neutral until `--detach`
exists: `serve` still runs one process, with the core and the client
talking over channels instead of struct fields. That keeps every
existing test meaningful and makes the socket transport a swap-in.

## Open questions going into implementation

1. Does the reload watch the config file (fsnotify) or stay explicit
   (`choragos reload` only)? Proposal: explicit only; a half-saved TOML
   converging automatically is worse than a deliberate verb.
2. Ring replay on attach re-renders final screens correctly for
   full-screen TUIs (they repaint on resize anyway, and the client sends
   a resize right after attach), but agents mid-animation may look torn
   until the next frame. Acceptable? Proposal: yes, note it in docs.
3. `choragos ls` output format and whether sessions get human names
   (directory basename) or just hashes. Proposal: basename + hash.
