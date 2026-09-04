# Design: deterministic check gate

An opt-in command gate in front of the judge: a delegated task must
pass a shell command (`go test ./...`, `helm unittest --failfast
charts/foo`, `terraform plan`) before anything else looks at it. A
failing check feeds the command's output back to the builder as the
critique and burns one round of the same loop the judge uses. The
cheap objective oracle rejects candidates that do not build or pass
tests before any judge tokens are spent, and the judge, when there is
one, only ever sees candidates that already pass. Never a silent pass.

Status: implemented as designed (`internal/deck/check.go`); the
contract and failure modes below are the behavior reference,
docs/configuration.md has the keys.

## Why a command, not a prompt

`judge_pass = 8` means another model scored the work 8 out of 10. That
is a subjective gate. Until now the only way to say "this must pass
the tests" was to write it into `prompt_template`, which turns a
deterministic check into a request the model may or may not honor.
For infrastructure work the objective oracle already exists and is
stronger than any model's opinion: an exit code.

## Configuration

```toml
[[roles]]
name = "coder"
command = "claude"
worktree = true
check = "helm unittest --failfast charts/foo"   # exit 0 = pass
check_timeout = "10m"                           # default 10m
judge = "reviewer"                              # optional; consulted only after check passes
judge_rounds = 3                                # caps check and judge rounds together
```

- `check` is run with `sh -c` in the role's worktree when
  `worktree = true`, otherwise in the workspace. Relative paths in the
  command resolve from that directory. Empty disables the gate.
- `check_timeout` bounds one run of the command (Go duration, default
  `10m`). The role's `timeout` is not reused: it bounds the delegation
  and its timer already cleared when work-done arrived.
- `judge_rounds` is the loop's round budget whatever the gate: check
  only, judge only, or both. A check failure and a judge failure each
  burn one round.

Load-time validation mirrors `timeout`: an unparseable or non-positive
`check_timeout` warns and falls back to the default. No `check` key
configured means no new code paths run: the delegate and work-done
flow is byte-for-byte today's, exactly as `judge` promised.

`check` is a config-file key only. The orchestrator's `roster-add`
proposal carries command, args, model and prompt (`internal/ipc`) and
cannot set `check`; a role that runs arbitrary shell on every
work-done must come from the file the user wrote, never from an agent.

## Composing with `judge` and `approve`

The check runs first. The order on every builder work-done is:

1. worktree commit (unchanged; retry rounds commit too)
2. ownership check (unchanged; a violation gates before anything runs)
3. `check`, when set
4. `judge`, when set
5. accept: orchestrator notified, merge mode runs

`approve` composes as it does with the judge: the human approves round
1, retry rounds do not re-enter the approve queue, and every loop exit
that is not a clean pass lands in the approve queue as a human gate.

## The check runs off the loop thread

`dispatch` runs on the deck's single update loop (Bubble Tea in TUI
mode, the server's message loop headless). A check that takes a
minute would freeze the UI and the control socket for a minute. The
command therefore runs in a goroutine and its result re-enters the
loop as a message, the same path `budgetMsg` uses. Loop state is not
touched from the goroutine; the message carries the task id, and the
handler re-looks the loop up by id when it lands. A loop that vanished
meanwhile (reload, role gone) is logged and dropped.

While the check runs the loop is in phase `check`. The builder's pane
is free; a second delegation to the same role is a separate loop, as
today.

## Critique contract: combined output, tail, report file

Test runners write failures to stdout (`go test`, `helm unittest`,
pytest) and compilers to stderr, so the critique is combined output,
not stderr alone. The last `checkOutputCap` (32 KiB) of it is written
to `.choragos/check-<taskid>-r<n>.log` with a one-line header naming
the command, the exit status and the directory. The retry task then
says, as the judge retry does:

```
Your previous attempt failed the check command (exit 1). Read
<path> for the output, fix the cause, and redo the task.

Original task:
<task>
```

The worker's task prompt also quotes the command ("run it yourself
first"), so a builder that can self-check rarely burns a round.

The tail is kept rather than the head because that is where runners
put the summary. Output is never injected into the pane; the file
path is, matching the verdict contract's reason: the PTY is not a
data channel.

## State model

The check reuses `judgeLoop` (`internal/deck/judge.go`): `phase`
gains the value `check`, and `maybeStartLoop` registers a loop when
the role sets `check` or `judge`.

```
delegate to coder (check and/or judge configured)
  round = 1; approve gate if configured (round 1 only)
  -> coder runs, work-done (report R1)
  -> check set: phase = check; run `sh -c check` in the role's dir
       exit 0                    -> judge set: judge round as today
                                    judge unset: task done, merge mode runs
       exit != 0, round < cap    -> round++; retry with the output file as critique
       exit != 0, round = cap    -> human gate "check cap exhausted", output attached
       cannot start / timed out  -> human gate "check unavailable", reason attached
  -> check unset: judge round as today
```

A check that cannot run (no `sh`, missing worktree, killed by the
timeout) is ambiguous, not a builder defect, so it fails closed to a
human gate rather than burning a round; this is the judge loop's
"judge unavailable" rule applied to the oracle. A non-zero exit is a
builder defect and retries.

Board and audit: the builder's delegate row is annotated with the
outcome in the existing `score` column (`check ok`, `check exit 1`),
and `events.log` gains `check` lines (`loop`, `round`, `exit`,
`verdict=pass|fail|unavailable`, `took`). No wire changes: the
`Round`, `Score` and gate `Reason` fields the judge loop added carry
everything.

## Failure modes

| Failure | Behavior |
|---------|----------|
| Check exits non-zero, rounds left | Retry round with the output file as critique |
| Check exits non-zero, cap reached | Human gate, reason "check cap exhausted", output attached |
| Check exceeds `check_timeout` | Process group killed; human gate, reason "check timed out after ..." |
| Check cannot start (`sh` missing, dir gone) | Human gate, reason "check unavailable: ..." |
| Builder role gone while the check runs | Human gate "builder role is gone", as the judge loop does |
| Builder unavailable for retry | Human gate, as the judge loop does |
| Config reload drops the loop mid-check | Result message finds no loop; logged and dropped |
| Server restart | Loop state lost with the session, as today; a running check is orphaned and its result discarded |
| No `check` configured | None of the above exists; today's flow exactly |

## Non-goals (v1)

- Running the check on judge rounds: the judge's report is prose, not
  code, and the judge role is not a builder.
- Per-check environment or shell selection; `sh -c` with the deck's
  environment, as the `[ui]` hooks already do.
- Streaming the check's output into a pane while it runs.
- Retrying a check that could not start.
- Feeding the check output to the judge in addition to the builder.

## Staging

1. Config key, `check_timeout` default and validation, unit tests.
2. Runner (`sh -c` in dir, timeout, output tail to file) plus the
   result message in both loops (TUI and server), unit tests for pass,
   fail-then-retry, cap exhaustion, timeout, unavailable, and the
   check-then-judge composition.
3. docs/configuration.md, docs/teams.md, docs/protocol.md, CHANGELOG.
