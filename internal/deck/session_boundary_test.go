// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The session core must stay UI-free so the v0.7 socket transport can move it
// out of this package without surgery; this pins the boundary until then.
func TestSessionCoreHasNoUIImports(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "session.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{
		"github.com/charmbracelet/bubbletea",
		"github.com/charmbracelet/lipgloss",
		"github.com/sphragis-oss/choragos/internal/wm",
	}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for _, b := range banned {
			if path == b {
				t.Errorf("session.go imports UI package %s; core must stay renderer-free", path)
			}
		}
	}
}
