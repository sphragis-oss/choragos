// SPDX-License-Identifier: Apache-2.0

package prompt_test

import (
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/prompt"
)

func testCfg() config.Config {
	return config.Config{Roles: []config.Role{
		{Name: "orchestrator", Start: true, Prompt: "You coordinate the team."},
		{Name: "coder", Prompt: "You implement changes."},
		{Name: "reviewer"},
	}}
}

func TestOrchestratorContext(t *testing.T) {
	c := prompt.OrchestratorContext(testCfg())
	for _, want := range []string{
		"You coordinate the team.", // start role's brief
		"## Available agents",
		"- **coder**",
		"- **reviewer**",
		"choragos delegate --to <role>",
		"choragos work-done",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("orchestrator context missing %q", want)
		}
	}
	if strings.Contains(c, "- **orchestrator**") {
		t.Error("start role must not list itself as an available agent")
	}
	if strings.Contains(c, "## Handoff for fresh agents") {
		t.Error("handoff section must be absent without fresh roles")
	}
}

func TestOrchestratorContextFreshHandoff(t *testing.T) {
	cfg := testCfg()
	cfg.Roles[1].Fresh = true
	c := prompt.OrchestratorContext(cfg)
	for _, want := range []string{
		"- **coder** (fresh: clean context every task)",
		"- **reviewer**\n",
		"## Handoff for fresh agents",
		".choragos/handoff-<role>.md",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("orchestrator context missing %q", want)
		}
	}
}

func TestWorkerTask(t *testing.T) {
	role := config.Role{Name: "coder", Prompt: "You implement changes."}
	c := prompt.WorkerTask(role, "Add the login endpoint at api/login.go", "T7", nil)
	for _, want := range []string{
		"You implement changes.",
		"## Task",
		"Task id: T7",
		"Add the login endpoint at api/login.go",
		"choragos work-done --id T7",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("worker task missing %q", want)
		}
	}
}

func TestWorkerBriefIdle(t *testing.T) {
	c := prompt.WorkerBrief(config.Role{Name: "auditor"}, nil)
	if !strings.Contains(c, "Stay idle until") {
		t.Errorf("worker brief should tell the agent to stay idle:\n%s", c)
	}
}

func TestJudgeTaskContract(t *testing.T) {
	role := config.Role{Name: "reviewer", Prompt: "You review code."}
	out := prompt.JudgeTask(role, "Build the widget", "/tmp/build-report.md", "/tmp/verdict.md", "T7", 8, nil)
	for _, want := range []string{
		"VERDICT: <n>/10",
		"8 or higher passes",
		"/tmp/verdict.md",
		"read /tmp/build-report.md",
		"choragos work-done --id T7 --report /tmp/verdict.md",
		"Build the widget",
		"You review code.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JudgeTask missing %q:\n%s", want, out)
		}
	}
}

func TestOwnershipClauses(t *testing.T) {
	owned := map[string]string{"defects.md": "qa", "plan.md": "lead"}

	qa := prompt.WorkerBrief(config.Role{Name: "qa"}, owned)
	if !strings.Contains(qa, "sole writer of: defects.md") {
		t.Errorf("owner brief lacks sole-writer clause:\n%s", qa)
	}
	if !strings.Contains(qa, "plan.md (owned by lead)") {
		t.Errorf("owner brief lacks other-owned files:\n%s", qa)
	}

	dev := prompt.WorkerTask(config.Role{Name: "dev"}, "fix bug", "T1", owned)
	if !strings.Contains(dev, "Never create, edit, or delete: defects.md (owned by qa), plan.md (owned by lead)") {
		t.Errorf("non-owner task lacks deny clause:\n%s", dev)
	}
	if strings.Contains(dev, "sole writer") {
		t.Errorf("non-owner task claims ownership:\n%s", dev)
	}

	cfg := config.Config{Roles: []config.Role{
		{Name: "orchestrator", Start: true},
		{Name: "qa", OwnsFiles: []string{"defects.md"}},
	}}
	orch := prompt.OrchestratorContext(cfg)
	if !strings.Contains(orch, "## File ownership") || !strings.Contains(orch, "defects.md (owned by qa)") {
		t.Errorf("orchestrator context lacks ownership map:\n%s", orch)
	}

	if out := prompt.WorkerBrief(config.Role{Name: "dev"}, nil); strings.Contains(out, "File ownership") {
		t.Errorf("clause rendered with no ownership configured:\n%s", out)
	}
}
