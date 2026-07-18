# Design: autonomous judge loop

An opt-in machine gate: a delegated task is scored by a judge role and
retried with the judge's critique until it passes or a round cap runs
out. The human `approve` gate stays exactly as it is; the judge loop is
the autonomous alternative for unattended runs, and every ambiguous or
failed outcome falls back to a human gate. Never a silent pass.

Status: implemented as designed; the verdict contract and failure modes
below are the behavior reference, docs/configuration.md has the keys.

## Configuration

The builder role declares its judge; the judge is an ordinary role with
no special marking, so any configured role can serve.

```toml
[[roles]]
name = "coder"
command = "claude"
judge = "reviewer"   # role that scores this role's completed work
judge_pass = 8       # minimum score (1-10) to pass; default 7
judge_rounds = 3     # max coder->judge rounds; default 3 (RestartCap idiom: 0 means default)

[[roles]]
name = "reviewer"
command = "agy"      # cross-vendor judging, per the teams.md policy
```

Load-time validation: `judge` must name a configured role and must not
name the role itself; `judge_pass` must be in 1..10; `judge_rounds`
follows the `RestartCap()` default-when-zero idiom via `JudgeCap()`.
`choragos doctor` warns (WARN, not FAIL) when a role and its judge
resolve to the same `command` + `model`: same-vendor self-agreement is
a known weakness, but enforcement is the user's call.

No `judge` key configured means no new code paths run: the delegate and
work-done flow is byte-for-byte today's.

## Composing with `approve`

Allowed, and the two gate different moments: `approve` gates entry
(before the worker runs), the judge gates exit (after work-done). With
both set on a role:

- The human approves round 1 as today.
- Retry rounds do NOT re-enter the approve queue. The human approved
  the task; the loop is the machinery they approved it into. Gating
  every retry would defeat the point of an autonomous loop.
- Every loop exit that is not a clean pass lands in the approve queue
  as a human gate (see failure modes), so the human sees every outcome
  that needs judgment, never every iteration.

## Verdict contract: report file, not pane text

The judge's verdict is never parsed from its terminal. Agent TUIs
repaint their viewport in place and the PTY is not a reliable data
channel (verified on #91: past content never reaches the byte stream).
The existing work-done report mechanism is the channel instead:

- The judge task file (written by `internal/prompt`, injected as the
  usual one-line `Read <file>`) instructs the judge to write its
  critique to a named report file and finish with
  `choragos work-done --report <file>`.
- The report file's first non-empty line must be exactly:

  ```
  VERDICT: <n>/10
  ```

  where `<n>` is an integer 0-10. Everything after that line is free
  text critique, fed to the builder on retry.
- Parsing is strict: one regex-free prefix check on one line of a file
  choragos wrote the path for. Anything else (missing line, prose
  around it, score out of range, unreadable file, work-done without
  `--report`) is an invalid verdict and fails closed.

Pass means `n >= judge_pass`. There is no separate pass/fail token, so
the contract cannot contradict itself.

## State model

Loop state lives in the session (`internal/deck/session.go`), keyed by
the task id the deck already assigns on delegate and echoes on
work-done. Per judged task: round counter, original task text, original
brief path, last judge report path.

```
delegate to coder (judge configured)
  round = 1; approve gate if configured (round 1 only)
  -> coder runs, work-done (report R1)
  -> deck synthesizes judge round: task file carries the original task,
     R1's path, the verdict contract, and the report path to write
  -> judge runs, work-done (report J1)
     VERDICT >= pass          -> task done; board shows rounds and final score
     VERDICT < pass, round<cap -> round++; deck synthesizes coder retry:
                                  original task + J1 critique path
     VERDICT < pass, round=cap -> human gate "judge cap exhausted"
     invalid verdict           -> human gate "unparseable verdict"
     judge timeout / exit      -> human gate "judge unavailable"
```

The deck becomes an actor here: retry and judge rounds are delegations
the deck synthesizes itself (today only agents delegate). They reuse
the existing delivery path (`deliverDelegate`), get ordinary task ids,
and are marked in `events.log` as `judge` kind lines
(`id`, `round`, `score`, `verdict=pass|fail|invalid`), so
`choragos report` and the audit trail see every round. Token and cost
accounting need no new code: rounds are normal per-role agent runs and
the gateway already attributes them.

Fallback human gates enter the existing `pendingGate` queue with a
reason and the last report path attached, so the gate modal (TUI and
desktop) can show why the loop stopped and what the judge last said.

## Wire changes

The board must show rounds and scores, so the `wire.Task` mirror gains
`Round int` and `Score string` (empty for unjudged tasks); the
`wire.Gate` mirror gains `Reason string` (empty for ordinary approve
gates). Additive fields, old clients ignore them; the exact-version
handshake makes mixed versions impossible anyway.

## Failure modes

| Failure | Behavior |
|---------|----------|
| Judge emits no/invalid `VERDICT:` line | Human gate, reason "unparseable verdict", judge report attached |
| Judge never calls work-done | Role `timeout` fires; for judge rounds the action is always the human-gate fallback, regardless of `timeout_action` |
| Judge role exited or gone | Human gate, reason "judge unavailable" |
| Round cap exhausted | Human gate, reason "judge cap exhausted", last critique attached; board marks the task |
| Builder retry crashes | Existing `restart`/timeout supervision, unchanged; the loop only advances on work-done |
| Deck detach/attach | Loop state survives (it lives in the session server); board resyncs rounds and scores over the wire |
| Server restart | Loop state is lost with the session, like gates today; events.log keeps the audit trail |
| No judge configured | None of the above exists; today's flow exactly |

## Non-goals (v1)

- Cross-vendor enforcement in code (doctor warns, user decides).
- Multi-judge quorum or judge-of-judges.
- Per-round rubric configuration beyond the pass threshold.
- Loop state persistence across server restarts.
- Judging work that does not flow through delegate/work-done.

## Staging

1. Config keys + validation + doctor warning, unit tests.
2. Verdict parsing + loop state machine in session.go, unit tests
   (malformed verdicts, cap exhaustion, fail-closed paths).
3. Prompt template for the judge task file.
4. Wire mirrors + board/gate rendering (TUI first, desktop follows).
5. docs/configuration.md, docs/teams.md, docs/protocol.md.
