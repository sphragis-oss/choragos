---
name: choragos-config
description: Compose a choragos team config (.choragos.toml) for a specific task by starting from the nearest in-repo template, applying only the non-obvious overrides (worktree and ownership, merge and approve gates, cross-vendor judge, budget and timeout, a deterministic check), and validating with choragos doctor. Use when the user asks to set up, tune, or sanity-check a choragos team for a piece of work. Not for choragos internals or bugs (work in the repo), and not a TOML generator (choragos init writes the base).
user-invocable: true
allowed-tools: Read, Edit, Write, Glob, Grep, Bash(choragos *), Bash(ls *), Bash(git *)
argument-hint: <the task and what makes it risky, in plain English>
---

# choragos-config

Three steps. The templates and `choragos doctor` do the heavy lifting; this skill only adds the judgment `init --auto` cannot make, which is the shape of the *risk*, not the language.

Reference for every key: `docs/configuration.md`. Do not invent keys; `doctor` reports unknown keys as typos.

## 1. Pick the base template, never write from scratch

| Situation | Command |
|---|---|
| No `.choragos.toml` yet, recognised project (go, node, python, rust, terraform, helm) | `choragos init --auto` |
| One of the shipped shapes fits | `choragos init --template <name>` |
| A config already exists | Read it; edit in place, do not regenerate |

Shipped shapes (`choragos init --template`): `starter` (commented walkthrough), `claude-team`, `mixed-team` (cross-vendor roles), `review` (read-only reporters), `worktree-flow` (parallel builders on branches), `defects-flow` (QA owns the defect ledger). `--auto` and `--template` are mutually exclusive; run `choragos init --help` for the current list.

## 2. Apply only the overrides the task earns

Ask these about the *task*, not the codebase. Each yes is one small edit; a no means leave the template alone.

| Question about the work | Override |
|---|---|
| Two or more roles will write to the same files? | `worktree = true` on each builder, plus `owns_files` for anything one role must hold the pen on (a ledger, a changelog, a schema) |
| Irreversible or wide blast radius (prod config, migrations, 40 charts at once)? | `merge = "gate"` (needs `worktree = true`) plus `approve = true` on the builder |
| The risk is the model agreeing with itself? | `judge = "<role>"` where the judge role has a different `command` or `model` vendor than the builder; `judge_pass = 8` for strict work |
| Long or unattended run? | `budget = "<USD>"` with `budget_action = "pause"`, and `timeout = "<duration>"` on every worker, `timeout` on the judge too |
| Is there a command that objectively decides pass or fail? | `check = "<command>"` on the builder (runs in its worktree on every work-done, exit 0 passes); `check_timeout` when the suite is slow |

The `check` is the strongest gate on the list and the cheapest: it rejects work before the judge spends a token. Reach for the real oracle, not a proxy:

```toml
check = "go build ./... && go test -race ./..."
check = "helm dependency update charts/foo && helm unittest --failfast charts/foo"
check = "terraform fmt -check -recursive && terraform init -backend=false -input=false >/dev/null && terraform validate"
```

Everything else stays as the template wrote it. No new roles unless the task names a job nobody on the roster does. Sphragis is optional: the config must work with the gateway present or absent, so never make a role depend on it.

## 3. Let doctor declare success

```bash
choragos doctor
```

The skill does not claim the config is right; `doctor` does. Read every `WARN` and `FAIL`:

- `judge:<role>` WARN, same command and model as the role it scores: change the judge's `command` or `model` to another vendor and run `doctor` again. Do not silence it by removing the judge.
- `config` WARN, unknown key: a typo or an invented key; fix the name against `docs/configuration.md`.
- `role:<role>` FAIL, command not in PATH: the agent binary is missing or aliased; use the real binary name.
- `sphragis` WARN, gateway off: acceptable, note it to the user, do not change the config to force it.

Stop when `doctor` prints no WARN or FAIL for the roles you touched. Report the file path, the overrides applied with one line of why each, and any WARN left on purpose.
