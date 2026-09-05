// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"os"
	"testing"

	"github.com/sphragis-oss/choragos/internal/ipc"
)

// TestMain drops an inherited CHORAGOS_SOCK so a test session never binds the socket of the deck running the tests.
func TestMain(m *testing.M) {
	_ = os.Unsetenv(ipc.EnvSocket)
	os.Exit(m.Run())
}
