// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

func TestProgramOptionsMouseToggle(t *testing.T) {
	if got := len(programOptions(config.Config{})); got != 3 {
		t.Fatalf("default options = %d, want 3 (alt screen, no signals, mouse)", got)
	}
	off := false
	cfg := config.Config{UI: config.UI{Mouse: &off}}
	if got := len(programOptions(cfg)); got != 2 {
		t.Fatalf("mouse=false options = %d, want 2 (no mouse capture)", got)
	}
}
