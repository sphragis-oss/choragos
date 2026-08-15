# Design: per-role git worktrees and merge gates

Covers issue [#189](https://github.com/sphragis-oss/choragos/issues/189)
(per-role worktree isolation with a gated merge back).

Status: stages 2 (worktree lifecycle) and 3 (deck-authored commits)
implemented; merge modes remain proposal. Deltas from the proposal:
`merge =` is not parsed yet, it arrives with the merge stage, and the
work-done commit subject carries the worker's summary rather than the
delegation label.

## Problem

Every role runs in the deck's working directory: `startRole`
(`internal/deck/session.go`) builds the `exec.Command` without setting
`cmd.Dir`, so all agents share one tree. Write ownership
(`internal/deck/ownership.go`) guards the coordination files listed in
`owns_files` by hashing them per task; the rest of the repository is a
free-for-all. Two roles editing the same source file is a race that
checkpoints make recoverable, not safe, and parallel delegations to two
coders on one repo are a guaranteed conflict.

The guardrail stack also gates the wrong artifact. `approve = true`
fires before execution, when there is only a plan to read. The thing a
human actually wants to hold is the diff, and today the diff does not
exist as an isolated object: by work-done time the changes are already
in the shared tree.

## Config surface

```toml
[[roles]]
name = "coder"
command = "claude"
worktree = true       # own git worktree per session
merge = "gate"        # gate | auto | manual (default manual)
```

Both keys are per role. Unconfigured roles keep today's behavior in the
shared tree; `merge` without `worktree = true` is a config error.

## Mechanism

### Placement and lifecycle

At spawn, a worktree role gets

```
git worktree add .choragos/worktrees/<role> -b choragos/<role> HEAD
```

- Path: under `contextDir`, so per-directory sessions keep everything
  in one place and the checkpoint exclude pathspec (which already
  omits `.choragos`) keeps worktrees out of snapshots for free.
- Branch: `choragos/<role>`, one per role, created from `HEAD` at
  session start. An existing branch from a previous session is reused
  (fast-forwarded to `HEAD` when fully merged, left alone otherwise so
  unmerged work survives a restart and resume finds it).
- The role's process spawns with `cmd.Dir` set to the worktree.

Worktrees persist across detach, quit, and resume: the session
snapshot records which roles own one, and resume revalidates against
`git worktree list` before respawning. Session end keeps them on disk
for post-mortem; a fully merged branch and its worktree are pruned at
the next session start.

### Task delivery

`deliverDelegate` writes `.choragos/worker-task-<role>.md` and injects
a relative "Read ..." line. Relative paths resolve inside the
worktree, where `.choragos` does not exist, so for worktree roles
every injected path (task file, briefs, fresh-role handoffs) becomes
absolute. This is the same fix class remote roles need (#109);
implementing it here pays that debt in shared code.

Owned coordination files stay in the main tree only, addressed by
absolute path. A worktree role that must *write* one (a QA role owning
`defects.md`) keeps that file outside its isolation: ownership and
worktrees compose, they do not replace each other.

### Deck-authored commits

Agents cannot be trusted to commit, and merge needs commits. At each
work-done from a worktree role, the deck commits the worktree onto the
role branch:

```
git -C .choragos/worktrees/<role> add -A
git -C .choragos/worktrees/<role> commit -m "choragos: <task-id> <label>"
```

using the same metadata style as checkpoint commits. The agent needs
no git discipline at all; every accepted task is one commit on the
role's branch, and the branch is the audit trail of who produced what,
task by task.

### Merge modes

Merging lands a role branch on the main tree's current branch.

- `manual` (default): choragos never merges. The branches are yours;
  `git merge choragos/coder` when you want it.
- `gate`: at work-done (after the judge loop accepts, when one is
  configured), a merge gate is queued. The gate shows the diffstat,
  `v` opens the full diff in the pager, `y` merges, `n` leaves the
  branch unmerged and tells the orchestrator why.
- `auto`: merge at accepted work-done without a gate, for trusted
  pipelines.

All modes share the rules:

- A conflict never auto-resolves: the merge aborts cleanly and falls
  closed to a gate carrying the conflicting paths, whatever the mode.
- A dirty main tree refuses the merge the same way: choragos never
  stacks its merge on top of uncommitted user work.
- The pre-merge state of the main tree is checkpointed through the
  existing store, so a bad merge is one rollback away.
- Ownership extends to the merge: an incoming diff touching a file
  some other role owns is held at an ownership gate, closing the gap
  where a worktree hides the violation from the work-done hash check.

### Judge loop composition

Nothing changes in the protocol: the judge scores the report as today.
With a worktree role, the deck can additionally attach the diff path
to the judge's task, so the score is grounded in the actual change
rather than the worker's claim. Merge happens only after the loop
accepts; a revise round keeps committing to the same branch.

### Degradation

`worktree = true` is an explicit request for isolation, so unlike
checkpoints it refuses loudly: no git on PATH or not a git repository
fails config validation at startup with the reason. `choragos doctor`
gains a line per worktree role. Everything works with the gateway on
or off.

## Non-goals (v1)

- Automatic conflict resolution, rebase strategies, or history
  rewriting. A conflict is a human's decision.
- Cross-worktree file sync during a task. Isolation is the point; the
  shared state is the main tree's `.choragos`.
- Submodules and nested repositories. Refused at validation for v1.
- Worktrees for the orchestrator. It plans and delegates, never
  implements; it stays in the main tree.
- Non-git backends.

## Staging

1. This design doc.
2. Worktree lifecycle: spawn in `cmd.Dir`, branch creation and reuse,
   absolute task paths, resume revalidation, doctor line, refusals.
3. Deck-authored commits at work-done.
4. Merge modes with the gate wired into the existing approval overlay,
   conflict and dirty-tree fail-closed paths, ownership-at-merge.
5. Checkpoint integration for pre-merge snapshots, docs
   (`configuration.md`, `teams.md`), and a `defects-flow` variant
   showing coder and adversary in parallel worktrees.
