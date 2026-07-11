# Control protocol

The deck and the `delegate` / `work-done` verbs talk JSON over a unix socket.
This page is the contract: everything a third-party tool needs to drive a
running deck without reading the Go source.

## Socket

Resolution order (both for the deck and the CLI verbs):

1. `$CHORAGOS_SOCK`, if set. The deck injects this into every role's
   environment, so agents inside panes always find their own deck.
2. `$XDG_RUNTIME_DIR/choragos.sock`, if `XDG_RUNTIME_DIR` is set.
3. `<temp dir>/choragos-<uid>.sock`.

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
| `cmd` | string | both | `"delegate"` or `"work-done"` |
| `to` | string array | delegate | Target role names; one injection per role |
| `task` | string | both | Task text (delegate) or one-line summary (work-done) |
| `brief` | string | delegate | Absolute path to a brief file holding the full task |
| `report` | string | work-done | Absolute path to a report file with the full outcome |
| `done` | bool | work-done | Marks the whole assignment complete |
| `id` | string | work-done | Task id echoed from the delegation, resolves the task-board entry |

The CLI verbs validate `brief` / `report` (non-empty regular file) and
absolutize them before sending, so they resolve from any working directory.
Unknown fields are ignored; new fields are always additive.

## What the deck does with a delegate

1. Refuses the command outright if the sphragis gateway is enforced and
   down (fail-closed): logged as `dispatch refused` in the event log.
2. Assigns a task id (`T1`, `T2`, ...) per target role.
3. Writes `.choragos/worker-task-<role>.md`: the role's `prompt_template`,
   the task id, the task text (pointing at `brief` when given), and the
   `work-done` instructions.
4. Types `Read .choragos/worker-task-<role>.md for your task.` into the
   role's PTY, then a separate Enter. Full prompts travel as files because
   multi-line pastes do not submit reliably in agent TUIs.

## What the deck does with a work-done

Routes it to the `start` role: `A worker reports: <summary>` plus
`Full report: read <report>` when a report path was sent. The `id` marks the
matching delegation resolved on the task board (prefix key, then `t`).

## Observability

- `.choragos/logs/events.log`: every delegate, work-done, boot injection,
  gateway refusal, and pane exit, as structured slog lines.
- `.choragos/logs/<role>.log`: each role's raw PTY output.
- The task board overlay shows the live delegation history with brief and
  report filenames per entry.

## Environment contract

Every role's process is started with:

- `CHORAGOS_SOCK`: the deck's control socket (see above).
- `ANTHROPIC_BASE_URL`: only when the gateway is enabled; carries an
  `/agent/<role>` suffix so token usage is attributed per role.

Roles with `env_allow` / `env_deny` still receive both; see
[configuration.md](configuration.md) for the isolation rules.
