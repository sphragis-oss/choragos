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
}

func TestWorkerTask(t *testing.T) {
	role := config.Role{Name: "coder", Prompt: "You implement changes."}
	c := prompt.WorkerTask(role, "Add the login endpoint at api/login.go")
	for _, want := range []string{
		"You implement changes.",
		"## Task",
		"Add the login endpoint at api/login.go",
		"choragos work-done",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("worker task missing %q", want)
		}
	}
}

func TestWorkerBriefIdle(t *testing.T) {
	c := prompt.WorkerBrief(config.Role{Name: "auditor"})
	if !strings.Contains(c, "Stay idle until") {
		t.Errorf("worker brief should tell the agent to stay idle:\n%s", c)
	}
}
