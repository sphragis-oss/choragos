# Design: session resume and orchestrator handoff

Status: Part A implemented; parts B and C proposed.
Two user-facing features with one shared
foundation: after a quit, `choragos serve --resume` brings the deck
back with its board, gates, layout, and roster intact; and
`choragos handoff` ends the current session on purpose, carrying its
context into a fresh session with a new (or the same) team. Both are
"deck state restore plus re-brief", not agent memory restore: the
agents' LLM context dies with their processes, and reviving it stays
agent-specific config (`--continue` in `args`), per the shipped
team-evolution design.

**The deck, not the agent, is the source of truth.** The roster, the
task board, task ids, pending gates, and the layout all live
deck-side already; the attach protocol's `welcome` frame is the
existing, wire-proven definition of "everything a fresh consumer
needs". Resume is that same snapshot, written to disk instead of a
socket.

## Today

Quit saves nothing. `session.closeAll()` closes the socket, kills
the panes, deletes the `ls` metadata sidecar, and exits; board,
gates, `taskSeq`, judge loops, roster tombstones, and the layout die
with the process. The only durable artifacts are the workspace
checkpoints (git refs, files only, `.choragos/` excluded), the
per-role transcript logs (append), and `events.log`, which the
*next* start truncates (`O_TRUNC`), destroying the previous run's
delegate/work-done history. Detach/attach is not resume: it needs
the server process alive.

## Part A: durable events.log

Smallest change, ships first, useful alone. Open `events.log` in
append mode and write a session-start marker line (timestamp, pid,
version) instead of truncating. `choragos report` learns to read
from the last marker by default, with `--all` for full history.
Rotation guard: if the file exceeds a cap (say 5 MB) on start,
rename to `events.log.1` and begin fresh, one level deep, no
rotation daemon.

This alone gives crash forensics across restarts and a history
source that resume can trust.

## Part B: session snapshot and --resume

### Snapshot

On every board mutation (delegate, work-done, gate open/resolve) and
on clean quit, the deck writes `.choragos/session.json`
atomically (temp file + rename):

```json
{
  "version": 1,
  "saved": "2026-08-02T10:04:05Z",
  "config_path": ".choragos.toml",
  "task_seq": 12,
  "roster": [{"name": "planner", "gone": false}, ...],
  "board": [...],
  "gates": [...],
  "layout": {...}
}
```

- `roster` records name and tombstone per index, in order. Order is
  load-bearing: wm leaves, frame indices, and wire roster rows are
  all int-indexed, so tombstoned slots must be preserved verbatim or
  every stored layout breaks.
- `board` and `gates` reuse the wire structs (`wire.Task`,
  `wire.Gate`); they are JSON already. `pendingGate` embeds the
  original `ipc.Command`, so a restored gate can resolve exactly as
  a live one does.
- `layout` is `Tree.Marshal()`, which exists.
- Judge loops and ownership snapshots are *not* persisted in v1: an
  in-flight judge round or owned-file hash set does not survive the
  quit. The recap tells the orchestrator which tasks were in flight;
  re-delegating them restarts those guards naturally. Recorded as an
  open question below.

Write frequency is one small JSON per board event, not per frame;
a deck doing nothing writes nothing.

### Resume

`choragos serve --resume` (and `--detach --resume`):

1. Load `.choragos/session.json`; refuse with a clear error if the
   version is unknown or the config path differs from the one given.
2. Verify against the config: every non-tombstoned roster name must
   exist in `cfg.Roles`. Extra config roles spawn as new (a reload
   would do the same); missing ones refuse resume, pointing at the
   config, since silently dropping a role would corrupt indices.
3. Start normally: spawn roles, restore `taskSeq`, board, gates, and
   hand the layout to the first client in `welcome` as attach does
   today.
4. The orchestrator's boot context gains the existing recap block,
   fed from the restored board instead of the (empty) live one: in
   flight ids, pending gate count, completed count, "do not
   re-delegate in-flight tasks".

In-flight tasks restore as in flight. The worker process behind them
is gone, so nothing will send `work-done`; the orchestrator sees
them in the recap and decides, with the timeout machinery as the
backstop exactly as when a worker is respawned mid-task today.

Without `--resume`, a leftover `session.json` is ignored and
overwritten on the first board event, preserving today's behavior.
A stale snapshot is never auto-applied.

### Failure modes

- Corrupt or unreadable snapshot: refuse `--resume` with the parse
  error; a plain `serve` still works.
- Snapshot from an older deck version: `version` field gates it;
  refuse with "snapshot from vX, this deck writes vY".
- Workspace changed since the quit: not resume's problem. The
  checkpoints cover file state; resume only restores deck state.

## Part C: choragos handoff

### Why not a hot swap

"The orchestrator always exists" is a design decision: removing the
start role or moving `start = true` at runtime stays refused.
Handoff therefore is not an in-place orchestrator replacement. It is
a deliberate session boundary: end this session, start the next one
with a different (or identical) team, and carry the context across
the boundary. Each session keeps one immutable orchestrator; the
sessions succeed each other.

### Flow

`choragos handoff [--config new-team.toml]`, plus a gated keybinding
in the deck:

1. The deck injects a request into the running orchestrator (the
   existing one-liner channel): write `.choragos/handoff-session.md`
   covering goal, state of the work, in-flight and remaining tasks,
   and anything the next team must know. This is the same
   agent-authored pattern as the per-role `handoff-<role>.md` that
   fresh workers use today, widened to the whole session.
2. The deck waits for the file to appear (bounded, say 120 s), then
   quits cleanly, writing the Part B snapshot as usual.
3. The next `choragos serve --resume` (with the new config if one
   was given) attaches `handoff-session.md` to the new
   orchestrator's boot context after the recap.

If the orchestrator never writes the file, the deck quits anyway
after the timeout and the next session gets the recap alone; the
handoff document is an enrichment, not a dependency. The user can
also write or edit `handoff-session.md` by hand, which composes
naturally since it is just a file the boot injection picks up.

Role-set changes ride entirely on the config file, the single
source of truth: the resume validation in Part B already defines
what happens when the new config adds or lacks roles. A handoff to a
disjoint team is simply a resume where most roster names are new.

### Configuration

None in v1. The verbs and the keybinding are always available; the
timeout is a constant until someone needs to tune it.

## Non-goals

- Agent LLM context restore. Per-agent resume flags in `args` plus
  the recap and handoff document cover it without coupling.
- Keeping agents alive across a deck restart. Agents are children of
  the deck; agent-survives-server stays out of scope as in the
  session-server design.
- Multi-snapshot history or named sessions. One snapshot per
  workspace, latest wins; the checkpoints and events.log are the
  history.
- Pane scrollback restore. The transcript logs already persist; the
  256 KiB raw ring is ephemeral by design.

## Delivery

Three PRs, in order, each useful alone:

1. events.log append + session markers + `report` range selection.
2. `session.json` snapshot writer, `--resume`, recap-from-snapshot,
   tests for the index-preservation and refusal paths.
3. `handoff` verb + keybinding + boot-context attachment of
   `handoff-session.md`, riding on 2.

## Open questions

- Should judge loops in flight block handoff (like gates block an
  orchestrator respawn today), or is "recap says T7 was mid-review"
  enough?
- Snapshot on every board event vs. debounced: is one write per
  event measurable on a busy deck, or fine forever?
- Should `--resume` be the default when a snapshot exists and the
  previous exit was clean, with `--fresh` to opt out? Start
  explicit, revisit after real use.
