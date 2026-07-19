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
| `cmd` | string | all | `"delegate"`, `"work-done"`, `"roster-add"`, or `"reload"` |
| `to` | string array | delegate | Target role names; one injection per role |
| `task` | string | both | Task text (delegate) or one-line summary (work-done) |
| `brief` | string | delegate | Absolute path to a brief file holding the full task |
| `report` | string | work-done | Absolute path to a report file with the full outcome |
| `done` | bool | work-done | Marks the whole assignment complete |
| `id` | string | work-done | Task id echoed from the delegation, resolves the task-board entry |
| `role_name` | string | roster-add | Proposed role name (letters, digits, `-`, `_`) |
| `role_command` | string | roster-add | Agent CLI the role runs; must resolve on PATH |
| `role_args` | string array | roster-add | Arguments for the command, in order |
| `role_model` | string | roster-add | Model for the role, optional |
| `role_prompt` | string | roster-add | `prompt_template` for the role, optional |

The CLI verbs validate `brief` / `report` (non-empty regular file) and
absolutize them before sending, so they resolve from any working directory.
Unknown fields are ignored; new fields are always additive.

`reload` carries no other fields: it asks the deck to re-read its config
file and converge the team on it (see
[configuration.md](configuration.md#reloading-the-config-at-runtime)).
It is accepted even while the gateway is down, because it changes the
team, not the work.

`roster-add` (sent by `choragos roster add`) proposes a new role. The
deck validates it (unique sanitized name, command on PATH, a config file
to extend, `[roster] propose` not disabled) and refuses invalid
proposals with an injected reason. Valid ones pause at a human gate
unless `[roster] approve = false`; on approval the deck appends the
`[[roles]]` block to the config file and runs the reload convergence,
so the file stays the single source of truth. The orchestrator hears
the outcome either way. Add-only by design for now: removals stay a
human edit plus `reload`.

Two more field-less verbs exist for session lifecycle: `ping` (liveness,
the ack is the answer; used by `choragos ls`) and `shutdown` (stop the
session and its agents; used by `choragos kill`).

## What the deck does with a delegate

1. Refuses the command outright if the sphragis gateway is enforced and
   down (fail-closed): logged as `dispatch refused` in the event log.
2. If the target role has `approve = true`, holds the delegation in the
   deck's approval overlay (logged as `delegate gated`) until the user
   presses `y` (proceed with the steps below) or `n` (inject a rejection
   notice into the orchestrator instead); `v` pages the attached brief
   in-app and `e` opens it in `$VISUAL`/`$EDITOR`, both leaving the
   gate pending.
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

## The judge loop (roles with `judge` set)

When the delegated role declares a `judge`, the deck runs the loop
itself and the orchestrator hears only outcomes:

1. The builder's `work-done` does not reach the orchestrator; the deck
   delegates a judge round instead (`delegate` logged with
   `from=choragos` and the loop id) whose task file carries the
   original task, the builder's report path, and the verdict contract.
2. The judge writes its critique to the named verdict file, whose
   FIRST non-empty line must be exactly `VERDICT: <n>/10` (integer
   0-10), and reports with `work-done --id <id> --report <file>`. The
   deck parses the file, never the judge's terminal.
3. Score at or above `judge_pass`: the orchestrator is told the task
   passed, with round, score, and verdict path.
   Below: the deck re-delegates to the builder with the critique
   (`judge retry` in the event log) until `judge_rounds` runs out.
4. Fail closed, always to a human gate: cap exhausted, unparseable or
   missing verdict, judge timeout (its role `timeout`, regardless of
   `timeout_action`), or judge exit. The gate shows the reason and the
   last report; approve hands the last result to the orchestrator,
   reject asks it to revise. Rounds and scores are stamped on the task
   board (`r2`, `6/10`), and `judge` lines in `events.log` carry
   loop id, round, score, and verdict.

## Observability

- `.choragos/logs/events.log`: every delegate, work-done, boot injection,
  gateway refusal, and pane exit, as structured slog lines. With the
  gateway on, cumulative per-role token counters are snapshotted into the
  log every 30s and on quit.
- `choragos report`: aggregates that log (or a saved copy passed as an
  argument) into a per-role table of tasks, completions, busy and average
  task time, first and last activity, and token usage; the token column
  reads n/a for roles the gateway never reported.
- `.choragos/logs/<role>.log`: each role's plain-text transcript, one
  session per header line with the working directory and start time.
  Lines are appended as they scroll off the live screen (every 15s), so
  long sessions survive in full; the live screen itself is flushed when
  the pane closes. A `--- transcript gap ---` marker means the screen
  was cleared or output outran the capture buffer between flushes.
  The contract is PTY-visible output: choragos records the bytes the
  agent writes to its terminal, nothing else. Full-screen agent TUIs
  (claude-code among them) repaint their viewport in place and never
  emit past content to the PTY, so their transcript is only what was
  last on screen, however long the session ran. That is not capture
  loss, the narrative never existed in the byte stream; for those
  roles the durable record is `events.log`, `choragos report`, and
  the per-task `--brief`/`--report` files.
- The task board overlay shows the live delegation history with brief and
  report filenames per entry.

## Environment contract

Every role's process is started with:

- `CHORAGOS_SOCK`: the deck's control socket (see above).
- `ANTHROPIC_BASE_URL`: only when the gateway is enabled; carries an
  `/agent/<role>` suffix so token usage is attributed per role.

Roles with `env_allow` / `env_deny` still receive both; see
[configuration.md](configuration.md) for the isolation rules.
