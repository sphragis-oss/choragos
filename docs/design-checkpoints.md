# Design: workspace checkpoints and rollback

Covers issue [#69](https://github.com/sphragis-oss/choragos/issues/69)
(workspace checkpoints per delegation with post-hoc rollback).

Status: proposal. Nothing here is implemented; staging is at the end.

## Problem

Agents sometimes wreck a working directory. Once a worker has run, the
only recovery is whatever git state the user happened to have: if the
agent overwrote uncommitted work or deleted untracked files, that work
is gone. The deck already knows the exact moment risk begins (the
delegation is handed to the worker, `deliverDelegate` in
`internal/deck/session.go`) and already names each unit of risk (the
task id `T<n>` on the board), so it can bracket every task with a
snapshot without the user doing anything.

Approval gates do not solve this: a gate fires before execution, when
there is nothing to judge yet. Checkpoints are the complement, recovery
after the result has landed and turned out to be bad. That is why every
delegation is checkpointed, gated or not, and why rollback is a
separate post-hoc action rather than an outcome of `n` on the gate.

## Non-goals (v1)

- Partial rollback (restore only the files task A touched while keeping
  task B's work). `git restore --source <ref> -- <path>` already gives
  that manually; building safe automatic file attribution on top of
  concurrent workers is a project of its own. Documented as manual.
- History manipulation. Rollback restores files; it never moves HEAD,
  never deletes branches or commits a worker created, never touches the
  user's index or stash.
- Non-git snapshot backends (tar, rsync). One well-tested mechanism; a
  directory that is not a git repository gets a startup warning and no
  checkpoints.
- Making `git` a hard dependency. Without git on PATH or without a
  repository, everything else keeps working.

## The mechanism: git plumbing on a temp index

A checkpoint is a real git commit object that no porcelain command ever
shows, created without touching the user's index, HEAD, or worktree:

```
GIT_INDEX_FILE=<tmpfile> git add -A
GIT_INDEX_FILE=<tmpfile> git write-tree            -> <tree>
git commit-tree <tree> -m <metadata>               -> <commit>
git update-ref refs/choragos/checkpoints/<name> <commit>
```

- `git add -A` on the temp index captures tracked and untracked files
  and respects `.gitignore`, so build artifacts and `node_modules`
  never bloat the object store. Unchanged files reuse existing blobs,
  so the incremental cost of a checkpoint is proportional to what
  changed since the last one, not to repository size.
- The commit has no parent (each checkpoint stands alone) and its
  message carries the metadata rollback needs: task id, target role,
  task label, dispatch time, and the HEAD sha at checkpoint time.
- The ref name is `<unix-epoch-of-dispatch>-<task-id>`, e.g.
  `refs/choragos/checkpoints/1783961035-T3`. Task ids restart at T1
  every session, so the epoch prefix keeps sessions from colliding and
  makes `git for-each-ref` output sort chronologically.

The checkpoint runs synchronously inside `deliverDelegate`, before the
task one-liner is injected into the worker's PTY: the worker cannot
have modified anything yet, so the snapshot is exactly the pre-task
state. A checkpoint failure (git missing, not a repository, plumbing
error) logs a warning to `events.log` and the delegation proceeds;
checkpointing is a safety net, never a gate.

Implementation shells out to the `git` binary (a new
`internal/checkpoint` package). No go-git dependency: the plumbing
surface used is four subcommands, the binary is already effectively
required for the projects choragos targets, and `choragos doctor`
gains a line reporting whether checkpoints will be active in this
directory.

## Rollback

Rollback makes the worktree match the checkpoint tree exactly, except
for ignored files, which are never touched:

1. **Checkpoint first.** The current state is snapshotted as
   `<epoch>-pre-rollback-<task-id>` before anything is restored, so a
   rollback is itself undoable. This is what makes a destructive
   operation safe to offer on a single keypress.
2. Restore content: `GIT_INDEX_FILE=<tmpfile> git read-tree <ckpt>`,
   then `git checkout-index -a -f` writes every file in the checkpoint
   over the worktree.
3. Delete extras: files present in the current state (per the same
   `add -A` view, so ignored files are exempt) but absent from the
   checkpoint tree are removed. Computed as a tree diff between the
   pre-rollback checkpoint and the target checkpoint, so the deletion
   list is derived from two immutable trees, not from a live scan.

What rollback deliberately leaves alone: HEAD, branches, the user's
index, the stash, and any commits the worker created. If a worker
committed and rollback restores older file content, the worktree will
show as dirty against the worker's commit; that is the correct,
inspectable representation of "the files went back, the history did
not". When HEAD at rollback time differs from the HEAD recorded in the
checkpoint metadata, the deck says so ("HEAD has moved since this
checkpoint: <old> -> <new>; files restored, history untouched") instead
of guessing.

## Surfaces

| Surface | Behavior |
|---|---|
| task board `u` | Roll back to the state before the selected task (board selection via `j`/`k` exists since the pager work). A confirm overlay in the gate style shows the task id, its age, and a summary of what will change (`N files restored, M deleted`); `y` proceeds, anything else cancels. |
| `choragos checkpoints` | List checkpoints for this directory: ref, task id, role, label, age. Reads refs directly, works with no session running. |
| `choragos rollback <task-id>` | CLI rollback, newest checkpoint matching the task id (full ref name accepted for older ones). Prints the same change summary and asks for confirmation; `--yes` skips it. Works with no session running: recovery must not require the thing that broke the workspace to still be alive. If a live session holds this directory, it warns that in-flight workers may be writing concurrently. |
| `choragos doctor` | Reports whether checkpoints are active here (git present, repository detected). |

## Retention

Checkpoints are cheap but not free, and refs pin objects against `git
gc`. Policy:

- Keep the newest `keep` checkpoints (default 20); prune older ones at
  session start, not at session end, because post-hoc rollback after a
  crashed or closed session is the main use case.
- `choragos checkpoints prune` forces the same cleanup manually.
- Deleting the ref is the whole cleanup; unreachable objects age out
  with normal `git gc`.

Config, one new table:

```toml
[checkpoints]
enabled = true   # per-delegation workspace snapshots (git repos only)
keep = 20        # newest checkpoints retained; older pruned at session start
```

## Answers to the open questions in #69

1. **Non-git working directories**: both proposed behaviors. One
   startup warning ("checkpoints disabled: not a git repository"),
   delegations proceed unprotected, and `rollback` refuses with the
   same message. Never `git init` implicitly: turning a directory into
   a repository is a user decision.
2. **Workers that create commits/branches**: rollback restores files
   and never touches history (see Rollback above); a moved HEAD is
   reported, not reverted. Worker commits stay recoverable by normal
   git means.
3. **Retention**: keep-newest-N with prune at session start plus a
   manual prune verb (see Retention).
4. **Partial rollback**: non-goal for v1; `git restore --source <ref>
   -- <path>` is the documented manual escape hatch.

## Open questions going into implementation

1. Checkpoint duration on large repos: the first checkpoint pays a full
   `add -A` scan. Measure on a big worktree; if it is slow enough to
   matter, the mitigation is logging the duration, not making the
   snapshot asynchronous (async would race the worker's first writes).
2. Should `work-done` also checkpoint (bracketing the task on both
   sides would let a summary say what the task changed)? Leaning no for
   v1: the pre-task snapshot is what rollback needs, and the board
   already links the report file.
3. Does the confirm overlay need a diffstat preview beyond the
   files-changed count? Leaning count-only for v1; `git diff <ref>` is
   one command away.

## Staging

| Stage | Ships | Why this order |
|---|---|---|
| PR 1 | `internal/checkpoint` (snapshot, list, prune, rollback), checkpoint-on-delegate, `[checkpoints]` config, `checkpoints`/`rollback` CLI verbs, doctor line | The whole mechanism is testable headless, no UI risk |
| PR 2 | task board `u` + confirm overlay, docs (configuration.md, teams.md, keybindings.md) | Pure client work on top of a proven core |

Both PRs fit one release. The CLI lands first so the feature is fully
exercisable (and recoverable) before any key binding exists.
