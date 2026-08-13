// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

func TestMemoryNoteOnlyWithContent(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &session{}
	if s.memoryNote() != "" {
		t.Fatal("no memory file: no note")
	}
	path := filepath.Join(contextDir, memoryFile)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if s.memoryNote() != "" {
		t.Fatal("empty memory file: no note")
	}
	if err := os.WriteFile(path, []byte("## 2026-08-13\n- tests need -tags integration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.memoryNote(), memoryFile) {
		t.Fatalf("memory present: note must point at it, got %q", s.memoryNote())
	}
	// and injectBoot lands it in the orchestrator context file
	e := remoteEntry("orc", true)
	s.cfg = config.Config{Roles: []config.Role{{Name: "orc", Start: true}}}
	s.panes = []*entry{e}
	s.injectBoot(e)
	ctx, err := os.ReadFile(filepath.Join(contextDir, "orchestrator-context.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), memoryFile) {
		t.Fatalf("orchestrator context must reference the memory file:\n%s", ctx)
	}
}
