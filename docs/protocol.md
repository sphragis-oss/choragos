# Control protocol

The deck and the `delegate` / `work-done` / `reload` verbs talk JSON over a
unix socket. This page is the contract: everything a third-party tool needs
to drive a running deck without reading the Go source.

## Socket

Sessions are per working directory. Resolution order (both for the deck and
the CLI verbs):

1. `$CHORAGOS_SOCK`, if set. The deck injects this into every role's
   environment, so agents inside panes always find their own deck.
2. `<runtime dir>/choragos-<uid>/<dir-hash>.sock`, where the runtime dir is
   `$XDG_RUNTIME_DIR` (or the temp dir) and `<dir-hash>` is 8 hex chars of
   the working directory's SHA-256, so one deck per project and paths that
   fit macOS's ~104-byte `sun_path` cap.

Run the CLI verbs from the project directory (or set `$CHORAGOS_SOCK`) so
they resolve the same session. A `<dir-hash>.ui.sock` next to it carries the
`choragos attach` client protocol (internal, versioned separately), and a
`<dir-hash>.json` sidecar feeds `choragos ls`.

The deck listens with file mode `0600` and removes a stale socket from a
crashed run at startup. On macOS, keep overrides short: `sun_path` caps unix
socket paths at roughly 104 characters, and long paths fail with
`bind: invalid argument`.

## Exchange

One command per connection: connect, send one JSON object, read one ack,
close. Both sides put a deadline on the whole exchange (5 seconds), so a
silent peer cannot hang a goroutine or the CLI.

```json
{"cmd":"delegate","to":["coder"],"task":"short label","brief":"/abs/path/brief.md"}
```

The deck replies:

```json
{"status":"ok"}
```

The ack means "delivered to the deck", not "the agent accepted it": routing
happens asynchronously in the UI loop.

## Command schema

| Field | Type | Used by | Meaning |
|-------|------|---------|---------|
| `cmd` | string | all | `"delegate"`, `"work-done"`, or `"reload"` |
| `to` | string array | delegate | Target role names; one injection per role |
| `task` | string | both | Task text (delegate) or one-line summary (work-done) |
| `brief` | string | delegate | Absolute path to a brief file holding the full task |
| `report` | string | work-done | Absolute path to a report file with the full outcome |
| `done` | bool | work-done | Marks the whole assignment complete |
| `id` | string | work-done | Task id echoed from the delegation, resolves the task-board entry |

The CLI verbs validate `brief` / `report` (non-empty regular file) and
absolutize them before sending, so they resolve from any working directory.
Unknown fields are ignored; new fields are always additive.

`reload` carries no other fields: it asks the deck to re-read its config
file and converge the team on it (see
[configuration.md](configuration.md#reloading-the-config-at-runtime)).
It is accepted even while the gateway is down, because it changes the
team, not the work.

Two more field-less verbs exist for session lifecycle: `ping` (liveness,
the ack is the answer; used by `choragos ls`) and `shutdown` (stop the
session and its agents; used by `choragos kill`).

## What the deck does with a delegate

1. Refuses the command outright if the sphragis gateway is enforced and
   down (fail-closed): logged as `dispatch refused` in the event log.
2. If the target role has `approve = true`, holds the delegation in the
   deck's approval overlay (logged as `delegate gated`) until the user
   presses `y` (proceed with the steps below) or `n` (inject a rejection
   notice into the orchestrator instead); `e` opens the attached brief
   in `$VISUAL`/`$EDITOR` first, leaving the gate pending.
3. Assigns a task id (`T1`, `T2`, ...) per target role.
4. Writes `.choragos/worker-task-<role>.md`: the role's `prompt_template`,
   the task id, the task text (pointing at `brief` when given), and the
   `work-done` instructions.
5. Types `Read .choragos/worker-task-<role>.md for your task.` into the
   role's PTY, then a separate Enter. Full prompts travel as files because
   multi-line pastes do not submit reliably in agent TUIs.

## What the deck does with a work-done

Routes it to the `start` role: `A worker reports: <summary>` plus
`Full report: read <report>` when a report path was sent. The `id` marks the
matching delegation resolved on the task board (prefix key, then `t`).

## Observability

- `.choragos/logs/events.log`: every delegate, work-done, boot injection,
  gateway refusal, and pane exit, as structured slog lines.
- `.choragos/logs/<role>.log`: each role's plain-text transcript (the
  rendered scrollback, written when the pane closes), one session per
  header line with the working directory and start time.
- The task board overlay shows the live delegation history with brief and
  report filenames per entry.

## Environment contract

Every role's process is started with:

- `CHORAGOS_SOCK`: the deck's control socket (see above).
- `ANTHROPIC_BASE_URL`: only when the gateway is enabled; carries an
  `/agent/<role>` suffix so token usage is attributed per role.

Roles with `env_allow` / `env_deny` still receive both; see
[configuration.md](configuration.md) for the isolation rules.
