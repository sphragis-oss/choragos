// SPDX-License-Identifier: Apache-2.0

// Project memory: an append-only .choragos/memory.md of decisions, gotchas,
// and conventions, offered to every orchestrator at boot. Hand-written today;
// agent-appended entries are a follow-up (#190).
package deck

import (
	"os"
	"path/filepath"
)

// memoryFile is the project knowledge file under contextDir.
const memoryFile = "memory.md"

// memoryNote points a booting orchestrator at the project memory file.
func (s *session) memoryNote() string {
	path := filepath.Join(contextDir, memoryFile)
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return ""
	}
	return "\n## Project memory\n\nRead " + path + " before delegating: decisions, gotchas, and conventions from previous sessions in this project.\n"
}
