# Design: write-partitioned coordination files

An opt-in separation-of-duties primitive: a role declares itself the
sole writer of specific coordination files, and choragos guards that
claim. The motivating shape is a shared `defects.md` that only the QA
role may write or close: coders read it and fix, but cannot mark their
own bug resolved, and the orchestrator cannot declare victory while a
defect is open. This attacks the "agent self-approves its own work"
failure the same way RBAC least-privilege attacks it for service
accounts. The judge loop (design-judge-loop.md) gives cross-model
verdicts on a task; ownership gives write-scoped coordination state.
They compose and neither changes the other's defaults.

Status: proposed, not implemented.

## Configuration

Ownership is declared on the owner, not as deny-lists on everyone
else: one place to read, impossible to leave a file half-denied.

```toml
[[roles]]
name = "qa"
command = "claude"
owns_files = ["defects.md"]   # workspace-relative literal paths
prompt_template = "Verify fixes against defects.md. Only you may edit it."
```

Load-time validation, all fail closed:

- Paths must be workspace-relative: no absolute paths, no `..`.
- A path may be claimed by exactly one role. Two roles claiming the
  same file is a contradiction, not a warning: `config.Load` rejects it.
- Literal paths only in v1; globs are a non-goal until a real team
  needs them (a glob also makes the "exactly one owner" check murky).

No `owns_files` key configured means no new code paths run: delegate,
work-done, and prompt building are byte-for-byte today's.

## Enforcement: three layers, two shipped

Honesty first: choragos injects prompts over a PTY and is never in the
agent's file-write path. Anything short of an OS sandbox is a tripwire,
not a wall. The design therefore layers:

### Layer 1: prompt guard (always on when ownership is declared)

`internal/prompt` gains an ownership clause, injected through the
existing task-file mechanism (files under `.choragos/`, one-line
`Read <file>` over the PTY):

- The owner's brief states it is the sole writer of its files.
- Every other role's brief and task file states the file is owned by
  `<role>` and must never be written, only read.
- `OrchestratorContext` lists the ownership map so the orchestrator
  routes edits to the owner instead of doing them itself.

No parsing of the delegated task text for write intent: guessing what
an agent will touch from prose is unreliable, and a false sense of
prevention is worse than none.

### Layer 2: detection (always on when ownership is declared)

The delegation lifecycle already has exactly two boundaries, and both
are single choke points:

- Entry: `deliverDelegate` (`internal/deck/session.go`), the one
  delivery path for orchestrator delegations, approved gates, and
  judge-synthesized rounds alike.
- Exit: the `work-done` case in `dispatch` (`internal/deck/session.go`).

At delivery, the session snapshots a hash of every owned file, keyed by
task id (missing file hashes to a sentinel, so creating or deleting an
owned file counts as a write). At work-done the hashes are re-checked:

```
owned file changed, delegate role is the owner        -> fine
owned file changed, delegate is not the owner,
  owner had no overlapping delegation                 -> violation
owned file changed, delegate is not the owner,
  owner also had an in-flight delegation              -> ambiguous
```

On violation the task does not resolve cleanly: it lands in the
existing `pendingGate` queue with reason "ownership violation" and the
file list attached, mirroring the judge loop's fallback gates, and an
`ownership` kind line goes to `events.log`. The orchestrator hears the
outcome only after the human rules on it. Never a silent pass.

Ambiguous outcomes are logged to `events.log` and surfaced to the
orchestrator as a warning line but do not gate: with concurrent
delegations the write cannot be attributed, and gating on guesses
teaches users to rubber-stamp the queue. This is a tripwire with
best-effort attribution, and the events line names every role that was
active in the window; it is not proof of who wrote.

Known limit, stated plainly: writes outside any delegation window
(the orchestrator's own hands, a role acting after work-done) are only
caught at the next delegation boundary, with correspondingly weaker
attribution. The prompt guard's orchestrator clause exists precisely
to keep the orchestrator's hands off owned files.

### Layer 3: OS enforcement (deferred, documented)

Real prevention is a per-role sandbox profile denying writes to owned
paths. Today choragos ships no sandbox code at all: docs/sandboxing.md
is a recipe the user wires up through `command`/`args` themselves, and
the seatbelt profile there is workspace-scoped, identical for every
role. Making choragos generate and manage per-role profiles is a new
subsystem (profile generation, command wrapping, collision with the
user's own wrappers), on top of macOS-only, Apple-deprecated
`sandbox-exec`, with bubblewrap/Landlock as a separate Linux effort.

v1 therefore does not build it. Instead docs/sandboxing.md gains a
per-role example: the workspace profile plus
`(deny file-write* (literal (param "OWNED")))` for non-owner roles,
so a user who wants the wall today can build it by hand. If a later
slice adds an `enforce` knob, the fail-closed rule is fixed now:
requesting OS enforcement on a platform that cannot provide it refuses
to start with a clear message. Falling back silently to layers 1+2
while claiming a file is protected is the one forbidden behavior.

## `choragos doctor`

One new WARN, alongside the existing judge self-agreement warning.
Detecting "mentions the file in a writing context" is not feasible, so
the check is simpler and honest: WARN whenever a non-owner's
`prompt_template` references an owned filename at all, with a message
saying the instruction may belong on the owner instead. Reading is
legitimate, hence WARN and not FAIL.
Duplicate claims are a load FAIL (see Configuration), so doctor
reports them through the existing config check.

## Composing with `approve` and the judge loop

Orthogonal by construction, and worth stating:

- `approve` gates entry, the judge gates exit quality, ownership gates
  who may write shared state. All three can sit on the same team.
- Judge rounds are delegations through `deliverDelegate`, so detection
  covers them with zero extra code: a coder retry that edits
  `defects.md` trips the same wire.
- Judge verdict report files live under choragos-chosen paths and are
  not coordination files; claiming paths under `.choragos/` is
  rejected at load.

## Failure modes

| Failure | Behavior |
|---------|----------|
| Non-owner delegation changed an owned file | Human gate, reason "ownership violation", files attached; `events.log` line |
| Change with overlapping owner delegation | `events.log` line + orchestrator warning, no gate (attribution ambiguous) |
| Owned file unreadable at snapshot or check | Treated as changed, fails toward the gate, never toward a silent pass |
| Write outside any delegation window | Caught at next boundary, attribution weak, logged as such |
| Two roles claim one file | `config.Load` error; deck refuses to start |
| Claim under `.choragos/` or absolute/`..` path | `config.Load` error |
| OS enforcement requested, platform lacks it (future knob) | Refuse to start with a clear message; never silent fallback |
| No `owns_files` configured | None of the above exists; today's flow exactly |

## Non-goals (v1)

- Generating or managing sandbox profiles (layer 3 stays a documented
  recipe; see above for the cost argument).
- Glob ownership patterns.
- Parsing task text for write intent.
- Attribution beyond delegation-window correlation (no fsnotify
  watcher, no per-process file tracing).
- Ownership of anything under `.choragos/`.

## Staging

1. `owns_files` config key + load validation, unit tests (duplicate
   claim, path escapes, `.choragos/` claim).
2. Snapshot/check plumbing in session.go keyed by task id, unit tests
   (violation, ambiguous overlap, sentinel create/delete, unreadable
   file fails closed).
3. Prompt clauses in `internal/prompt` (owner brief, non-owner brief,
   orchestrator context), unit tests.
4. Gate + `events.log` + board wiring, reusing the judge loop's
   `pendingGate` reason mechanics.
5. doctor WARN + docs/configuration.md, docs/teams.md.
6. `defects-flow.toml` starter template (orchestrator + coder +
   QA-owns-defects.md + adversary), mirroring `mixed-team.toml`.
7. docs/sandboxing.md per-role deny example (the manual layer 3).
