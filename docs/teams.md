# Building your own team

The deck runs whatever team `.choragos.toml` describes: any number of roles,
each its own agent binary, model, prompt, and environment. This page is the
guide; [configuration.md](configuration.md) is the key-by-key reference and
[protocol.md](protocol.md) the wire contract.

## Start from a template

```bash
choragos init --template starter      # commented single-agent scaffold
choragos init --template claude-team  # all-Claude 5-role crew
choragos init --template mixed-team   # Claude + Gemini mix
choragos init --template review       # review-focused team
```

Then `choragos doctor` to verify binaries resolve and the gateway is
reachable, and `choragos serve` to start.

## Anatomy of a role

```toml
[[roles]]
name = "coder"                # delegate --to coder
command = "claude"            # real binary on PATH, not a shell alias
model = "opus"                # appended as --model when set
prompt_template = """
Implement the requested change. Run the project's tests before reporting done.
"""
```

- Exactly one role sets `start = true`: the orchestrator. It receives the
  delegation protocol at boot and all `work-done` reports.
- `prompt_template` is injected at boot and prefixed to every delegated
  task, so it is the place for the role's standing orders.
- Every role runs in a PTY the deck owns; you can type into any pane
  directly at any time.

## Pick models per role, not per team

The role table is where cost control lives: pin expensive models only where
judgment quality gates the outcome, cheap ones everywhere else.

| Job | Model class | Why |
|-----|-------------|-----|
| Planning / orchestration | strongest | plan quality gates everything downstream |
| Implementation | strongest | a model upgrade beats doubling the token budget |
| Structured extraction, summaries, packaging | small | accuracy tasks, not brilliance tasks |
| Review / audit | mid, ideally another vendor | cross-model diversity catches more |

With `[pricing]` set, the sidebar cards show live cost per role, so the
matrix is measurable, not aspirational.

## Context hygiene: keep roles lean

Every role is a full agent CLI session that inherits your personal
configuration, and with several roles the leak is multiplicative: every
turn of every role carries the whole prefix. A fresh idle claude role can
start at ~40% context before any task arrives. Run `/context` inside a
pane to see the breakdown; a reported real-world case:

| Source | Tokens | Lever |
|--------|--------|-------|
| Claude Code system prompt + built-in tools | ~44k | none: the baseline every session pays |
| Personal MCP servers (39 tools) | ~25k | `args = ["--strict-mcp-config"]` |
| Global CLAUDE.md + memory imports | ~10k | prune your global config; run the deck from a clean directory |
| Custom agents + skills | ~3k | `--disable-slash-commands` for skills |
| The choragos boot brief | ~1.5k | already minimal |

The templates ship workers with `--strict-mcp-config` (with no
`--mcp-config` it loads no MCP servers at all): a delegated worker rarely
needs your personal MCP servers, and the flag alone cuts the reported case
from ~42% to ~30%. Delete it from a role that genuinely needs them; the
orchestrator keeps them by default.

`claude --bare` trims further (no auto-memory, no CLAUDE.md discovery, no
hooks) but authenticates strictly via `ANTHROPIC_API_KEY`, so it does not
work with subscription OAuth logins.

## Long tasks travel as files

Keep `--task` for one-liners. For real work, write a brief file (objective,
acceptance criteria, references by path, out of scope) and delegate it:

```bash
choragos delegate --to coder --brief /abs/path/brief-T7.md --task "T7: auth middleware"
```

Workers reply in kind:

```bash
choragos work-done --id T7 --report /abs/path/report-T7.md --task "done, 3 files, tests green"
```

Content moves through files, control through the socket: the orchestrator's
context stays small, and every task leaves an auditable artifact.

## Approval gates: a human between plan and execution

Set `approve = true` on a role and every delegation to it pauses in the
deck: an overlay shows the target, task, and brief path, the bell rings,
and nothing reaches the worker until you press `y`. Pressing `n` rejects
and tells the orchestrator to revise its plan.

```toml
[[roles]]
name = "coder"
command = "claude"
model = "opus"
approve = true
```

Because briefs are files, amending is free: read the brief the overlay
points at, edit it in another terminal, then approve; the worker reads the
corrected file. Gates queue in arrival order, the status line counts them,
and approvals and rejections land in `events.log`. Use it on the roles
whose mistakes are expensive (implementation, release), and leave
read-only reporters ungated.

## Isolate credentials per role

A reviewer does not need your cloud keys:

```toml
[[roles]]
name = "reviewer"
command = "agy"
model = "Gemini 3.1 Pro (High)"
env_deny = ["AWS_*", "GITHUB_TOKEN"]
```

`env_allow` flips a role to an allowlist (baseline vars plus what you list);
`env_deny` strips matches in either mode and always wins.

## Taming a chatty agent TUI

Two per-role knobs feed the status heuristics:

- `input_prompts`: extra substrings that mean "blocked waiting for input",
  for agents whose confirmation prompts the built-ins miss. The card turns
  orange and the bell rings on the transition.
- `chrome_markers`: substrings marking statusline/footer noise so the
  sidebar activity preview shows real output instead of progress bars.

## A worked example: a delivery pipeline team

```toml
[[roles]]
name = "orchestrator"
command = "claude"
model = "opus"
start = true
prompt_template = "You coordinate the pipeline. Plan, delegate with briefs, never implement."

[[roles]]
name = "analyser"
command = "claude"
model = "sonnet"
prompt_template = "Turn input documents into a structured spec. Report with work-done --report."

[[roles]]
name = "coder"
command = "claude"
model = "opus"
prompt_template = "Implement each brief with its tests. Run them before reporting."

[[roles]]
name = "reviewer"
command = "agy"
model = "Gemini 3.1 Pro (High)"
env_deny = ["AWS_*"]
prompt_template = "Review diffs against the spec. Findings only; never edit."

[[roles]]
name = "packager"
command = "claude"
model = "haiku"
prompt_template = "Assemble PR descriptions and summaries from diffs and reports."
```

Roles are fixed for the lifetime of the deck; restart one in place with the
prefix key plus `R`.
