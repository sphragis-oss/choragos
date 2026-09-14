// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/ipc"
)

// sampleLine is the exposition grammar for one sample: name, optional escaped labels, a number.
var sampleLine = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{[a-zA-Z_][a-zA-Z0-9_]*="(\\.|[^"\\])*"(,[a-zA-Z_][a-zA-Z0-9_]*="(\\.|[^"\\])*")*\})? -?[0-9]+(\.[0-9]+)?$`)

func renderText(t *testing.T, ms *metricsServer, snap *metricsSnapshot) string {
	t.Helper()
	var b strings.Builder
	ms.render(&b, snap)
	return b.String()
}

func TestMetricsCountersFollowTheBoard(t *testing.T) {
	orc, coder := remoteEntry("orc", true), remoteEntry("coder", false)
	coder.role.Timeout = "1s"
	coder.role.Budget = "2.50"
	old := remoteEntry("old", false)
	old.gone = true
	ghost := remoteEntry("coder", false) // tombstone of an earlier coder; the live one must win
	ghost.gone = true
	s := &session{cfg: config.Config{Pricing: map[string]config.Price{"claude": {Input: 1}}}, panes: []*entry{orc, ghost, coder, old}}

	s.recordTask(taskEvent{at: time.Now().Add(-3 * time.Second), kind: "delegate", id: "T1", to: "coder"})
	s.recordTask(taskEvent{at: time.Now().Add(-2 * time.Second), kind: "delegate", id: "T2", to: "coder"})
	s.recordTask(taskEvent{at: time.Now(), kind: "work-done", id: "T1", to: "orc"})
	s.resolveTask("T1")
	s.checkTimeouts() // T2 outlived the 1s timeout
	s.metrics.setScore("coder", 9, true)
	s.metrics.setScore("coder", 4, false)
	bump(&s.metrics.check, labelKey{"coder", "pass"})
	bump(&s.metrics.merges, labelKey{"coder", "merged"})
	bump(&s.metrics.restarts, "coder")
	s.gates = []pendingGate{
		{cmd: ipc.Command{Cmd: "delegate"}, to: "coder"},
		{to: "coder", reason: "merge x", mergeID: "T1"},
		{to: "coder", reason: "changed defects.md", ownership: true},
		{to: "coder", reason: "judge cap exhausted"},
		{cmd: ipc.Command{Cmd: "roster-add"}, to: "orc"},
	}
	s.lastUsage = usageMsg{"coder": {In: 10, Out: 5, CacheCreation: 2, CacheRead: 3, Cost: 0.25}}
	coder.paused = true
	coder.overBudget = true

	snap := s.metricsSnapshot(time.Now())
	if got := len(snap.roles); got != 3 {
		t.Fatalf("roles = %d, want 3 (coder, old, orc): %+v", got, snap.roles)
	}
	if snap.roles[0].name != "coder" || snap.roles[0].state != "paused" || snap.roles[1].state != "exited" || snap.roles[2].state != "idle" {
		t.Fatalf("roles = %+v", snap.roles)
	}
	if snap.openTasks != 1 {
		t.Fatalf("openTasks = %d, want 1", snap.openTasks)
	}
	for _, k := range gateKinds {
		if snap.gates[k] != 1 {
			t.Fatalf("gates[%s] = %d, want 1", k, snap.gates[k])
		}
	}

	ms := &metricsServer{version: "1.2.3", mode: "server", started: time.Unix(100, 0)}
	out := renderText(t, ms, snap)
	for _, want := range []string{
		`choragos_info{version="1.2.3",mode="server"} 1`,
		`choragos_session_start_time_seconds 100`,
		`choragos_role_state{role="coder",state="paused"} 1`,
		`choragos_role_state{role="coder",state="working"} 0`,
		`choragos_role_state{role="old",state="exited"} 1`,
		`choragos_role_tasks_total{role="coder"} 2`,
		`choragos_role_tasks_total{role="orc"} 0`,
		`choragos_role_tasks_done_total{role="coder"} 1`,
		`choragos_role_tasks_timed_out_total{role="coder"} 1`,
		`choragos_role_restarts_total{role="coder"} 1`,
		`choragos_role_tokens_total{role="coder",direction="input"} 10`,
		`choragos_role_tokens_total{role="coder",direction="cache_read"} 3`,
		`choragos_role_cost_usd{role="coder"} 0.25`,
		`choragos_role_budget_usd{role="coder"} 2.5`,
		`choragos_role_over_budget{role="coder"} 1`,
		`choragos_role_over_budget{role="orc"} 0`,
		`choragos_judge_rounds_total{role="coder",verdict="fail"} 1`,
		`choragos_judge_rounds_total{role="coder",verdict="pass"} 1`,
		`choragos_check_runs_total{role="coder",verdict="pass"} 1`,
		`choragos_merges_total{role="coder",outcome="merged"} 1`,
		`choragos_judge_score{role="coder"} 4`,
		`choragos_tasks_open 1`,
		`choragos_gates_pending{kind="merge"} 1`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	busy := regexp.MustCompile(`choragos_role_busy_seconds_total\{role="coder"\} ([0-9.]+)`).FindStringSubmatch(out)
	if busy == nil || busy[1] < "2" {
		t.Fatalf("busy seconds for coder should be about 3s: %v", busy)
	}
	if strings.Contains(out, "choragos_gateway_up") || strings.Contains(out, `budget_usd{role="orc"}`) || strings.Contains(out, `tokens_total{role="orc"`) {
		t.Fatalf("series must be absent, not zero, when unmeasured:\n%s", out)
	}
	if strings.Count(out, `role="coder",state="paused"`) != 1 {
		t.Fatalf("tombstone of a live role must not duplicate its series:\n%s", out)
	}
	checkExposition(t, out)
}

// checkExposition asserts every line is a comment or a grammatical sample, each TYPE is declared once
// before its samples, and no sample repeats a label set.
func checkExposition(t *testing.T, out string) {
	t.Helper()
	typed := map[string]bool{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			name := strings.Fields(line)[2]
			if typed[name] {
				t.Fatalf("TYPE declared twice: %s", name)
			}
			typed[name] = true
			continue
		}
		if strings.HasPrefix(line, "# HELP ") {
			continue
		}
		if !sampleLine.MatchString(line) {
			t.Fatalf("not a valid sample line: %q", line)
		}
		key := line[:strings.LastIndex(line, " ")]
		name, _, _ := strings.Cut(key, "{")
		if !typed[name] {
			t.Fatalf("sample before its TYPE: %q", line)
		}
		if seen[key] {
			t.Fatalf("duplicate sample: %q", key)
		}
		seen[key] = true
	}
}

func TestMetricsRenderNilSnapshotAndGateway(t *testing.T) {
	ms := &metricsServer{version: "dev", mode: "tui", started: time.Unix(7, 0)}
	out := renderText(t, ms, nil)
	if !strings.Contains(out, `choragos_info{version="dev",mode="tui"} 1`) || strings.Contains(out, "choragos_role_state") {
		t.Fatalf("nil snapshot must render the header series only:\n%s", out)
	}
	checkExposition(t, out)

	s := &session{sphragisOn: true, gatewayUp: true, panes: []*entry{remoteEntry("orc", true)}}
	s.lastUsage = usageMsg{"orc": {In: 1}}
	out = renderText(t, ms, s.metricsSnapshot(time.Now()))
	if !strings.Contains(out, "choragos_gateway_up 1\n") || !strings.Contains(out, `choragos_role_tokens_total{role="orc",direction="input"} 1`) {
		t.Fatalf("gateway series missing:\n%s", out)
	}
	if strings.Contains(out, "choragos_role_cost_usd") {
		t.Fatalf("cost must be absent without a [pricing] table:\n%s", out)
	}
	checkExposition(t, out)
}

func TestMetricsEscapesLabels(t *testing.T) {
	s := &session{panes: []*entry{remoteEntry("a\"b\\c\nd", true)}}
	out := renderText(t, &metricsServer{}, s.metricsSnapshot(time.Now()))
	if !strings.Contains(out, `choragos_role_over_budget{role="a\"b\\c\nd"} 0`) {
		t.Fatalf("label not escaped:\n%s", out)
	}
	checkExposition(t, out)
}

func TestGateKind(t *testing.T) {
	for _, tc := range []struct {
		g    pendingGate
		want string
	}{
		{pendingGate{cmd: ipc.Command{Cmd: "delegate"}}, "approve"},
		{pendingGate{cmd: ipc.Command{Cmd: "roster-add"}}, "roster"},
		{pendingGate{reason: "judge timed out"}, "judge"},
		{pendingGate{reason: "wrote defects.md", ownership: true}, "ownership"},
		{pendingGate{reason: "merge", mergeID: "T2"}, "merge"},
	} {
		if got := gateKind(tc.g); got != tc.want {
			t.Errorf("gateKind(%+v) = %q, want %q", tc.g, got, tc.want)
		}
	}
}

func TestMetricsServerLifecycle(t *testing.T) {
	s := &session{cfg: config.Config{Metrics: config.Metrics{Listen: "127.0.0.1:0"}}, mode: "server"}
	s.publishMetrics() // no server yet: must be a no-op
	s.startMetrics()
	addr := s.metricsAddr()
	if addr == "" {
		t.Fatal("metrics did not start")
	}
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := get("/metrics")
	if code != 200 || !strings.Contains(body, `mode="server"`) || strings.Contains(body, "choragos_role_state") {
		t.Fatalf("before the first tick: code=%d body:\n%s", code, body)
	}
	s.panes = []*entry{remoteEntry("orc", true)}
	s.publishMetrics()
	if _, body = get("/metrics"); !strings.Contains(body, `choragos_role_state{role="orc",state="idle"} 1`) {
		t.Fatalf("published snapshot not served:\n%s", body)
	}
	if code, _ = get("/nope"); code != 404 {
		t.Fatalf("unknown path = %d, want 404", code)
	}
	resp, err := http.Post("http://"+addr+"/metrics", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("POST = %d, want 405", resp.StatusCode)
	}
	s.stopMetrics()
	if s.metricsAddr() != "" {
		t.Fatal("addr must clear after stop")
	}
	if _, err := http.Get("http://" + addr + "/metrics"); err == nil {
		t.Fatal("listener still answering after stop")
	}
	s.stopMetrics() // idempotent
}

func TestMetricsStartRefusesBadListen(t *testing.T) {
	s := &session{}
	s.startMetrics()
	if s.metricsSrv != nil {
		t.Fatal("empty listen must not start a server")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	s.cfg.Metrics.Listen = ln.Addr().String()
	s.startMetrics()
	if s.metricsSrv != nil {
		t.Fatal("a taken port must leave the deck without metrics, not panic or retry")
	}
}

func TestReloadReopensMetrics(t *testing.T) {
	m, path := reloadFixture(t, reloadBase)
	if m.metricsAddr() != "" {
		t.Fatal("fixture must start without metrics")
	}
	if err := os.WriteFile(path, []byte(reloadBase+"\n[metrics]\nlisten = \"127.0.0.1:0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.reloadConfig()
	addr := m.metricsAddr()
	if addr == "" {
		t.Fatal("reload with a new [metrics] listen must open the endpoint")
	}
	if err := os.WriteFile(path, []byte(reloadBase), 0o600); err != nil {
		t.Fatal(err)
	}
	m.reloadConfig()
	if m.metricsAddr() != "" {
		t.Fatal("reload without [metrics] must close the endpoint")
	}
	if _, err := http.Get("http://" + addr + "/metrics"); err == nil {
		t.Fatal("old listener still answering after reload")
	}
}
