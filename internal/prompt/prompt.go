// SPDX-License-Identifier: Apache-2.0

// Package prompt builds role boot and task prompts; full content goes to a file since Claude Code's TUI drops multi-line PTY pastes.
package prompt

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sphragis-oss/choragos/internal/config"
)

// ownershipClause renders the file-ownership rules for one role; empty when nothing is owned.
func ownershipClause(roleName string, owned map[string]string) string {
	if len(owned) == 0 {
		return ""
	}
	var mine, theirs []string
	for _, p := range slices.Sorted(maps.Keys(owned)) {
		if owned[p] == roleName {
			mine = append(mine, p)
		} else {
			theirs = append(theirs, fmt.Sprintf("%s (owned by %s)", p, owned[p]))
		}
	}
	var b strings.Builder
	b.WriteString("## File ownership\n\n")
	if len(mine) > 0 {
		b.WriteString("You are the sole writer of: " + strings.Join(mine, ", ") + ". Every other role only reads them; route their change requests through yourself.\n")
	}
	if len(theirs) > 0 {
		b.WriteString("Never create, edit, or delete: " + strings.Join(theirs, ", ") + ". Read them freely; ask the owner for any change. Changes by non-owners are detected and stop the task.\n")
	}
	b.WriteString("\n")
	return b.String()
}

// OrchestratorContext is the start role's boot context: brief, available roles, and delegation protocol.
func OrchestratorContext(cfg config.Config) string {
	var b strings.Builder
	start := ""
	for _, r := range cfg.Roles {
		if r.Start {
			start = r.Name
			if r.Prompt != "" {
				b.WriteString(r.Prompt)
				b.WriteString("\n\n")
			}
			break
		}
	}
	b.WriteString(ownershipClause(start, cfg.OwnedFiles()))
	b.WriteString("## Available agents\n\n")
	fresh := false
	for _, r := range cfg.Roles {
		if r.Start {
			continue
		}
		if r.Fresh {
			fresh = true
			fmt.Fprintf(&b, "- **%s** (fresh: clean context every task)\n", r.Name)
			continue
		}
		fmt.Fprintf(&b, "- **%s**\n", r.Name)
	}
	b.WriteString("\n## Delegation protocol\n\n")
	b.WriteString("Delegate with one command per agent (run via your shell):\n\n")
	b.WriteString("```bash\nchoragos delegate --to <role> --task \"Task with full context, file paths, and constraints.\"\n```\n\n")
	b.WriteString("For anything longer than a couple of sentences, write a brief file (objective, acceptance criteria, references by path, out of scope) and hand over the file instead; keep --task as a short label:\n\n")
	b.WriteString("```bash\nchoragos delegate --to <role> --brief /abs/path/to/brief.md --task \"Short label.\"\n```\n\n")
	b.WriteString("Delegate to several agents in parallel by making one call each. Never delegate to a role not listed above. Wait for a worker's work-done before delegating to it again. When the whole assignment is validated:\n\n")
	b.WriteString("```bash\nchoragos work-done --done --task \"Final summary.\"\n```\n\n")
	if fresh {
		b.WriteString("## Handoff for fresh agents\n\nFresh agents remember nothing between tasks. Keep a short handoff per fresh role in .choragos/handoff-<role>.md: decisions made, files touched, gotchas. Update it after each of their work-done reports; it is attached to their next task automatically.\n\n")
	}
	if cfg.Roster.CanPropose() {
		b.WriteString("## Team changes\n\nIf the assignment needs a role the team lacks, propose one; the user approves it before it joins, and you will be told the outcome:\n\n")
		b.WriteString("```bash\nchoragos roster add --name <role> --command <agent-cli> [--model <model>] [--prompt-template \"role brief\"]\n```\n\n")
	}
	b.WriteString("## Important\n\nWait for the user to tell you what to work on, then delegate immediately. Do not implement, review, or audit yourself. Before the release step, stop and let the user confirm end to end.\n")
	return b.String()
}

// WorkerBrief is a worker's boot context: its role brief, ownership rules, and the idle protocol.
func WorkerBrief(role config.Role, owned map[string]string) string {
	var b strings.Builder
	if role.Prompt != "" {
		b.WriteString(role.Prompt)
		b.WriteString("\n\n")
	}
	b.WriteString(ownershipClause(role.Name, owned))
	b.WriteString("## Protocol\n\nStay idle until the orchestrator delegates a task to you. When you get one, complete it, then report:\n\n")
	b.WriteString("```bash\nchoragos work-done --task \"Summary with file paths and outcomes.\"\n```\n")
	return b.String()
}

// JudgeTask is a judge round's prompt: score the builder's work with a strict machine-readable verdict.
func JudgeTask(role config.Role, task, builderReport, verdictFile, id string, pass int, owned map[string]string) string {
	var b strings.Builder
	if role.Prompt != "" {
		b.WriteString(role.Prompt)
		b.WriteString("\n\n")
	}
	b.WriteString(ownershipClause(role.Name, owned))
	b.WriteString("## Judge task\n\nTask id: " + id + "\n\nYou are the judge for this delegated task:\n\n" + task + "\n\n")
	if builderReport != "" {
		b.WriteString("The worker's report: read " + builderReport + "\n\n")
	}
	b.WriteString("Review the actual work against the task, not just the report.\n\n")
	fmt.Fprintf(&b, "## Verdict (strict format)\n\nWrite your review to %s. The FIRST line of that file must be exactly:\n\n```\nVERDICT: <n>/10\n```\n\nwhere <n> is an integer 0-10; %d or higher passes. After that line, write the critique: what is wrong, what to change, ordered by importance.\n\n", verdictFile, pass)
	b.WriteString("Then report:\n\n```bash\nchoragos work-done --id " + id + " --report " + verdictFile + " --task \"Judged: one-line verdict summary.\"\n```\n")
	return b.String()
}

// WorkerTask is a worker's task prompt: role brief, ownership rules, the task, and the work-done instruction.
func WorkerTask(role config.Role, task, id string, owned map[string]string) string {
	var b strings.Builder
	if role.Prompt != "" {
		b.WriteString(role.Prompt)
		b.WriteString("\n\n")
	}
	b.WriteString(ownershipClause(role.Name, owned))
	b.WriteString("## Task\n\n")
	if id != "" {
		b.WriteString("Task id: " + id + "\n\n")
	}
	b.WriteString(task)
	idFlag := ""
	if id != "" {
		idFlag = " --id " + id
	}
	b.WriteString("\n\n## When done\n\n```bash\nchoragos work-done" + idFlag + " --task \"Summary with file paths and outcomes.\"\n```\n")
	b.WriteString("\nIf the outcome needs more than a line, write it to a report file and add --report /abs/path/to/report.md; keep --task as the one-line summary.\n")
	return b.String()
}
