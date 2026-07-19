# Design: live team evolution

Status: Part A implemented (orchestrator swap on reload, with the
recap carried in the boot context file rather than a separate
injection); Part B is a proposal (issue #141). Extends the shipped
roles-at-runtime
mechanism (`choragos reload` / `prefix+C`, v0.6.0, #24): workers can
already be added, removed, and respawned live by editing the config
and reloading. Two gaps remain, and one principle frames both:

**The orchestrator always exists.** It is the heart of the deck. It
can change shape at runtime; it never disappears. Removing the start
role and reassigning `start = true` to another role stay refused,
now as a design decision rather than a limitation.

## Part A: orchestrator model swap on reload

### Today

`reload` treats the start role as frozen: spec changes (command,
model, args, env identity) log "restart the deck to apply", removal
is refused, reassignment is ignored (internal/deck/session.go,
`reload`). The rationale was that the orchestrator's LLM context,
what it delegated and what it awaits, dies with its process.

### Proposal

Respawn the start role like any worker when its spec changes, then
rebuild its working context from what the deck already knows. The
deck, not the agent, is the source of truth for team state: the
roster, the task board, in-flight task ids, and pending gates all
live deck-side and survive the respawn untouched.

After the respawn and the normal boot injection, the deck injects a
recap line built from that state:

```
[choragos] Respawned with a new spec. Team: coder, reviewer. In
flight: T7 -> coder. Pending gates: 1. Completed this session: 5.
Continue coordinating; do not re-delegate in-flight tasks.
```

- Work-done matches by task id, so in-flight delegations complete
  normally into the new process's view.
- The existing in-flight guard stays: while the orchestrator itself
  is mid-conversation with a gate (a pending approval it must hear
  about), the respawn is skipped with the same "rerun once resolved"
  warning workers get today.
- Agent-native resume composes through config, not code: the same
  edit that changes `model` can add the agent's resume flag (for
  example `--continue`) to `args`. Choragos stays agent-agnostic;
  the recap is the generic baseline that works for every CLI.

### Failure modes

- New command not found: keep the old process, log an error. Same as
  workers today.
- Respawn fails to start: auto-restart budget applies; worst case
  the user restarts with `prefix+R`. The deck itself never depends
  on the orchestrator process being alive.

## Part B: orchestrator-proposed roles

### Idea

The orchestrator plans the work, so it is the first to notice the
team is wrong for it: "this task needs a security reviewer", "the
translator has been idle all session". Today only the human can act
on that. Give the orchestrator verbs to propose roster changes, with
a human gate by default. Proposals are advice; the human stays in
charge of the team.

### Verbs

Two new IPC commands, shaped like `delegate`:

```
choragos roster add --name sec-reviewer --command claude --model sonnet \
  [--prompt-template "..."]
choragos roster remove --name translator
```

Validation at the socket, rejection with a reason the orchestrator
sees (injected line): duplicate or invalid name, command not on
PATH, remove of the start role, remove of a role with tasks in
flight.

### Gate

Reuses the approval-gate overlay with a `roster` variant, like the
judge fallback gates: the card shows the proposed spec, `y` applies,
`n` rejects and the verdict is injected back to the orchestrator.
Every proposal and outcome lands in events.log for the audit trail.

### Source of truth

The config file stays the single source of truth, otherwise the next
`reload` would silently retire a runtime-added role. Applying an add
appends the `[[roles]]` block to `.choragos.toml` with a one-line
marker comment, then runs the normal reload convergence, so file and
live roster never diverge. Running on the built-in config (no file)
refuses roster verbs, exactly as `reload` does today.

Remove is the harder half: deleting a block from a user-formatted
TOML file is text surgery. v1 ships add-only unless review finds a
safe remove (see open questions); a rejected remove still tells the
orchestrator why, so it can ask the human in plain language instead.

### Configuration

```toml
[roster]
propose = true   # orchestrator may propose roster changes
approve = true   # human gate on proposals; false = auto-apply
```

Defaults on/on: the capability is present out of the box, and a
proposal never touches the team without a human `y`. Disabling
`propose` removes the verbs' line from the orchestrator's boot
context and refuses the commands at the socket. Auto-apply is a
deliberate opt-in for unattended runs, mirroring the judge loop's
philosophy: autonomy is opt-in, ambiguity falls to a human.

### Boot context

With `propose` on, the orchestrator's boot injection gains one line
describing the verbs and the gate, so the capability is discoverable
by the agent without prompt engineering by the user.

## Non-goals

- Removing the orchestrator or moving `start = true` at runtime.
- A TUI form for composing roles in-deck: the config file plus
  `prefix+C` already covers human-driven edits.
- Agent-specific context restore (session files, resume APIs): the
  recap plus user-configured resume args cover it without coupling.

## Delivery

Part A is small and independent: lift the start-role freeze, add the
recap builder, extend the reload tests. Part B builds on the gate
plumbing: verbs in cmd/choragos + ipc.Command fields, gate variant,
config append, boot-context line, docs. Separate PRs in that order.

## Open questions

- Safe `roster remove` file edit: comment the block out instead of
  deleting it? Ship add-only first?
- Recap length: cap the in-flight list (first N ids) so a large
  board cannot flood the new orchestrator's first screen.
- Should a rejected proposal cool down (no identical re-proposal for
  the session) to stop a loop of asking?
