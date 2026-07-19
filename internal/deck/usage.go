// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sphragis-oss/choragos/internal/config"
)

// roleUsage is one role's token tally and priced cost, as read from the gateway.
type roleUsage struct {
	In, Out, CacheCreation, CacheRead int64
	Cost                              float64
	Model                             string // dominant model by token volume
}

// usageMsg carries the latest per-role usage snapshot, keyed by role name.
type usageMsg map[string]roleUsage

// tokenLine matches the gateway's sphragis_tokens_total exposition lines.
var tokenLine = regexp.MustCompile(`^sphragis_tokens_total\{agent="([^"]*)",model="([^"]*)",direction="([^"]*)"\} ([0-9.eE+]+)$`)

// fetchUsage reads the gateway metrics off the UI thread; failures keep the last snapshot.
func fetchUsage(addr string, pricing map[string]config.Price) tea.Cmd {
	return func() tea.Msg {
		client := http.Client{Timeout: time.Second}
		resp, err := client.Get("http://" + addr + "/metrics")
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil
		}
		return parseUsage(string(body), pricing)
	}
}

// tokenSnapInterval paces the cumulative token snapshots written to the event log.
const tokenSnapInterval = 30 * time.Second

// maybeLogTokens rate-limits event-log token snapshots; the fetch runs off the loop thread.
func (s *session) maybeLogTokens() {
	if !s.sphragisOn || !s.gatewayUp || time.Since(s.lastTokens) < tokenSnapInterval {
		return
	}
	s.lastTokens = time.Now()
	go s.logTokens()
}

// logTokens writes one cumulative token line per role so the report survives quit,
// and feeds the priced costs back to the loop thread for budget enforcement.
func (s *session) logTokens() {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(s.cfg.Sphragis.BaseURL() + "/metrics")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return
	}
	costs := budgetMsg{}
	for role, u := range parseUsage(string(body), s.cfg.Pricing) {
		if u.Cost > 0 {
			s.log().Info("tokens", "role", role, "in", u.In, "out", u.Out,
				"cache_creation", u.CacheCreation, "cache_read", u.CacheRead,
				"cost", fmt.Sprintf("%.4f", u.Cost))
		} else {
			s.log().Info("tokens", "role", role, "in", u.In, "out", u.Out,
				"cache_creation", u.CacheCreation, "cache_read", u.CacheRead)
		}
		costs[role] = u.Cost
	}
	if len(costs) > 0 {
		s.send(costs)
	}
}

// parseUsage aggregates token metric lines into per-agent tallies and priced cost.
func parseUsage(body string, pricing map[string]config.Price) usageMsg {
	out := usageMsg{}
	perModel := map[string]map[string]int64{}
	for _, line := range strings.Split(body, "\n") {
		m := tokenLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		agent, model, dir := m[1], m[2], m[3]
		v, err := strconv.ParseFloat(m[4], 64)
		if err != nil || agent == "" {
			continue
		}
		n := int64(v)
		if perModel[agent] == nil {
			perModel[agent] = map[string]int64{}
		}
		perModel[agent][model] += n
		u := out[agent]
		p, priced := priceFor(model, pricing)
		switch dir {
		case "input":
			u.In += n
			u.Cost += cost(n, priced, p.Input)
		case "output":
			u.Out += n
			u.Cost += cost(n, priced, p.Output)
		case "cache_creation":
			u.CacheCreation += n
			u.Cost += cost(n, priced, p.CacheCreation)
		case "cache_read":
			u.CacheRead += n
			u.Cost += cost(n, priced, p.CacheRead)
		}
		out[agent] = u
	}
	for agent, u := range out {
		u.Model = dominantModel(perModel[agent])
		out[agent] = u
	}
	return out
}

// dominantModel picks the model with the most tokens; ties break lexicographically for determinism.
func dominantModel(tokens map[string]int64) string {
	var best string
	max := int64(-1)
	for m, n := range tokens {
		if n > max || (n == max && m < best) {
			best, max = m, n
		}
	}
	return best
}

// prettyModel humanizes claude model ids (claude-opus-4-8-20260115 -> Claude Opus 4.8); other ids pass through.
func prettyModel(id string) string {
	if !strings.HasPrefix(id, "claude-") {
		return id
	}
	parts := strings.Split(id, "-")
	if last := parts[len(parts)-1]; len(last) == 8 && allDigits(last) {
		parts = parts[:len(parts)-1] // date suffix
	}
	var words, nums []string
	for _, p := range parts {
		switch {
		case p == "":
		case allDigits(p):
			nums = append(nums, p)
		default:
			words = append(words, strings.ToUpper(p[:1])+p[1:])
		}
	}
	s := strings.Join(words, " ")
	if len(nums) > 0 {
		s += " " + strings.Join(nums, ".")
	}
	return s
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func cost(n int64, priced bool, perMtok float64) float64 {
	if !priced {
		return 0
	}
	return float64(n) * perMtok / 1e6
}

// priceFor finds the longest pricing key that prefixes the model name.
func priceFor(model string, pricing map[string]config.Price) (config.Price, bool) {
	var best string
	var found bool
	var out config.Price
	for key, p := range pricing {
		if strings.HasPrefix(model, key) && len(key) > len(best) {
			best, out, found = key, p, true
		}
	}
	return out, found
}

// agentURL appends the /agent/<role> attribution prefix when the name is path-safe.
func agentURL(baseURL, role string) string {
	if baseURL == "" || role == "" || len(role) > 64 || !pathSafe(role) {
		return baseURL
	}
	return baseURL + "/agent/" + role
}

func pathSafe(name string) bool {
	for _, r := range name {
		ok := r == '.' || r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// roleModel prefers the model the gateway actually saw over the configured one.
func (m *Model) roleModel(r config.Role) string {
	if u, ok := m.usage[r.Name]; ok && u.Model != "" {
		return prettyModel(u.Model)
	}
	return prettyModel(r.Model)
}

// usageLabel renders a compact card line: token arrows plus cost when priced.
func (u roleUsage) usageLabel() string {
	if u.In+u.Out+u.CacheCreation+u.CacheRead == 0 {
		return ""
	}
	s := "↑" + formatTokens(u.In+u.CacheCreation+u.CacheRead) + " ↓" + formatTokens(u.Out)
	if u.Cost > 0 {
		s += " " + formatCost(u.Cost)
	}
	return s
}

// formatCost keeps sub-cent amounts visible instead of rounding to $0.00.
func formatCost(c float64) string {
	if c < 0.01 {
		return fmt.Sprintf("$%.4f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

// formatTokens humanizes a token count (999, 1.2k, 3.4M).
func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e3), ".0") + "k"
	default:
		return strconv.FormatInt(n, 10)
	}
}
