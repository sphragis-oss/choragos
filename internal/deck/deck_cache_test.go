// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

func TestScreenCachesServeUntilSeqAdvances(t *testing.T) {
	cfg := config.Config{Roles: []config.Role{
		{Name: "a", Command: "sh", Args: []string{"-c", "printf role-alpha"}},
	}}
	panes, err := startPanes(cfg, 40, 6, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, e := range panes {
			_ = e.pane.Close()
		}
	}()
	e := panes[0]
	waitStream(t, e.pane)

	if got := e.renderCached(); !strings.Contains(got, "role-alpha") {
		t.Fatalf("first render missing output:\n%q", got)
	}
	e.cacheRender = "SENTINEL"
	if got := e.renderCached(); got != "SENTINEL" {
		t.Fatal("unchanged pane must serve the render cache")
	}
	e.renderSeq-- // pretend a new chunk landed
	if got := e.renderCached(); got == "SENTINEL" {
		t.Fatal("seq change must force a re-render")
	}

	tail := e.tailCached()
	if len(tail) == 0 || !strings.Contains(tail[len(tail)-1], "role-alpha") {
		t.Fatalf("first tail scan missing output: %q", tail)
	}
	e.cacheTail = []string{"SENTINEL"}
	if got := e.tailCached(); len(got) != 1 || got[0] != "SENTINEL" {
		t.Fatal("unchanged pane must serve the tail cache")
	}
	e.tailSeq-- // pretend a new chunk landed
	if got := e.tailCached(); len(got) == 0 || got[0] == "SENTINEL" {
		t.Fatal("seq change must force a tail re-scan")
	}
}

func TestCachesRefreshAfterRestartSwapsPane(t *testing.T) {
	m := newTestModel(startCatPanes(t, "solo"))
	e := m.panes[0]
	e.cacheRender = "STALE"
	e.cacheTail = []string{"STALE"}
	e.renderPane, e.tailPane = nil, nil // a restart leaves caches pointing at another pane
	if got := e.renderCached(); got == "STALE" {
		t.Fatal("pane swap must invalidate the render cache")
	}
	if got := e.tailCached(); len(got) == 1 && got[0] == "STALE" {
		t.Fatal("pane swap must invalidate the tail cache")
	}
}

func TestLastN(t *testing.T) {
	in := []string{"a", "b", "c"}
	if got := lastN(in, 2); len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("lastN(3,2) = %q", got)
	}
	if got := lastN(in, 5); len(got) != 3 {
		t.Fatalf("lastN(3,5) = %q", got)
	}
	if got := lastN(nil, 2); len(got) != 0 {
		t.Fatalf("lastN(nil,2) = %q", got)
	}
}
