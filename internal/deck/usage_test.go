// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

const sampleMetrics = `# HELP sphragis_tokens_total Model tokens.
# TYPE sphragis_tokens_total counter
sphragis_tokens_total{agent="coder",model="claude-sonnet-5",direction="input"} 200
sphragis_tokens_total{agent="coder",model="claude-sonnet-5",direction="output"} 50
sphragis_tokens_total{agent="coder",model="claude-sonnet-5",direction="cache_read"} 1000000
sphragis_tokens_total{agent="reviewer",model="gpt-4o",direction="input"} 10
sphragis_tokens_total{agent="",model="claude-sonnet-5",direction="input"} 999
sphragis_requests_total{path="/v1/messages",upstream="anthropic"} 3
`

func TestParseUsage(t *testing.T) {
	pricing := map[string]config.Price{
		"claude-sonnet": {Input: 3, Output: 15, CacheRead: 0.3},
	}
	got := parseUsage(sampleMetrics, pricing)
	coder, ok := got["coder"]
	if !ok {
		t.Fatalf("missing coder: %v", got)
	}
	if coder.In != 200 || coder.Out != 50 || coder.CacheRead != 1000000 {
		t.Fatalf("coder tally = %+v", coder)
	}
	// 200*3/1e6 + 50*15/1e6 + 1e6*0.3/1e6
	want := 0.0006 + 0.00075 + 0.3
	if diff := coder.Cost - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("coder cost = %v, want %v", coder.Cost, want)
	}
	// unpriced model tallies tokens with zero cost
	if rev := got["reviewer"]; rev.In != 10 || rev.Cost != 0 {
		t.Fatalf("reviewer = %+v", rev)
	}
	// empty agent label is dropped
	if _, ok := got[""]; ok {
		t.Fatal("empty agent should be dropped")
	}
}

func TestPriceForLongestPrefix(t *testing.T) {
	pricing := map[string]config.Price{
		"claude":          {Input: 1},
		"claude-sonnet-5": {Input: 3},
	}
	p, ok := priceFor("claude-sonnet-5-20250929", pricing)
	if !ok || p.Input != 3 {
		t.Fatalf("priceFor = %+v, %v", p, ok)
	}
	if _, ok := priceFor("gemini-pro", pricing); ok {
		t.Fatal("gemini should not match")
	}
}

func TestAgentURL(t *testing.T) {
	cases := []struct{ base, role, want string }{
		{"http://127.0.0.1:8787", "coder", "http://127.0.0.1:8787/agent/coder"},
		{"http://127.0.0.1:8787", "bad role", "http://127.0.0.1:8787"},
		{"http://127.0.0.1:8787", strings.Repeat("x", 65), "http://127.0.0.1:8787"},
		{"", "coder", ""},
	}
	for _, c := range cases {
		if got := agentURL(c.base, c.role); got != c.want {
			t.Errorf("agentURL(%q, %q) = %q, want %q", c.base, c.role, got, c.want)
		}
	}
}

func TestRoleEnvAgentBaseURL(t *testing.T) {
	env := roleEnv(config.Role{Name: "coder"}, "/tmp/s.sock", "http://127.0.0.1:8787")
	found := false
	for _, kv := range env {
		if kv == "ANTHROPIC_BASE_URL=http://127.0.0.1:8787/agent/coder" {
			found = true
		}
	}
	if !found {
		t.Fatalf("role env missing agent base URL: %v", env)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{{0, "0"}, {999, "999"}, {1000, "1k"}, {1234, "1.2k"}, {2_500_000, "2.5M"}}
	for _, c := range cases {
		if got := formatTokens(c.n); got != c.want {
			t.Errorf("formatTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestUsageLabel(t *testing.T) {
	if got := (roleUsage{}).usageLabel(); got != "" {
		t.Fatalf("empty usage should hide the label, got %q", got)
	}
	u := roleUsage{In: 1200, Out: 50, CacheRead: 3_000_000, Cost: 0.42}
	got := u.usageLabel()
	for _, want := range []string{"↑3M", "↓50", "$0.42"} {
		if !strings.Contains(got, want) {
			t.Errorf("label %q missing %q", got, want)
		}
	}
	// sub-cent costs stay visible
	small := roleUsage{In: 100, Out: 7, Cost: 0.0027}
	if got := small.usageLabel(); !strings.Contains(got, "$0.0027") {
		t.Errorf("small cost label = %q", got)
	}
}

func TestCardsShowUsage(t *testing.T) {
	panes := startCatPanes(t, "orchestrator", "coder")
	m := newTestModel(panes)
	m.usage = usageMsg{"coder": {In: 1500, Out: 200, Cost: 0.05}}
	view := m.View()
	for _, want := range []string{"↑1.5k", "↓200", "$0.05"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}
