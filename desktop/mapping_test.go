// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/wire"
)

func TestParityFieldMappings(t *testing.T) {
	r := toRoles([]wire.Role{{Role: config.Role{Name: "coder"}, OverBudget: true}})
	if len(r) != 1 || !r[0].OverBudget {
		t.Fatalf("roles = %+v, want OverBudget", r)
	}
	tk := toTasks([]wire.Task{{Kind: "delegate", TimedOut: true}})
	if len(tk) != 1 || !tk[0].TimedOut {
		t.Fatalf("tasks = %+v, want TimedOut", tk)
	}
}
