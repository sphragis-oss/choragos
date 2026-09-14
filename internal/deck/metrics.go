// SPDX-License-Identifier: Apache-2.0

// Live Prometheus text endpoint: the loop thread publishes a snapshot, the
// handler renders the copy and never touches session state (docs/design-metrics.md).
package deck

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// labelKey addresses one labeled counter: a role plus a verdict, outcome or direction.
type labelKey struct{ role, label string }

// metricsCounters are the session's monotonic counts, bumped beside the matching events.log line.
type metricsCounters struct {
	tasks, done, timedOut, restarts map[string]int
	busy                            map[string]time.Duration
	judge, check, merges            map[labelKey]int
	score                           map[string]int // last valid judge score per builder
}

// bump increments a lazily allocated counter map.
func bump[K comparable](m *map[K]int, k K) {
	if *m == nil {
		*m = map[K]int{}
	}
	(*m)[k]++
}

func (c *metricsCounters) addBusy(role string, d time.Duration) {
	if c.busy == nil {
		c.busy = map[string]time.Duration{}
	}
	c.busy[role] += d
}

func (c *metricsCounters) setScore(role string, score int, pass bool) {
	if c.score == nil {
		c.score = map[string]int{}
	}
	c.score[role] = score
	bump(&c.judge, labelKey{role, map[bool]string{true: "pass", false: "fail"}[pass]})
}

func (c metricsCounters) clone() metricsCounters {
	return metricsCounters{
		tasks: maps.Clone(c.tasks), done: maps.Clone(c.done), timedOut: maps.Clone(c.timedOut), restarts: maps.Clone(c.restarts),
		busy: maps.Clone(c.busy), judge: maps.Clone(c.judge), check: maps.Clone(c.check), merges: maps.Clone(c.merges), score: maps.Clone(c.score),
	}
}

// roleMetrics is one role's gauges at snapshot time.
type roleMetrics struct {
	name       string
	state      string
	budget     float64
	overBudget bool
}

// metricsSnapshot is a pointer-free copy of everything /metrics renders, built on the loop thread.
type metricsSnapshot struct {
	gatewayOn, gatewayUp bool
	priced               bool
	roles                []roleMetrics
	openTasks            int
	gates                map[string]int
	counters             metricsCounters
	usage                usageMsg
}

var (
	metricStates = []string{"working", "waiting", "idle", "paused", "exited"}
	gateKinds    = []string{"approve", "judge", "ownership", "merge", "roster"}
	directions   = []string{"input", "output", "cache_creation", "cache_read"}
)

// gateKind classifies a pending gate for the gates_pending series.
func gateKind(g pendingGate) string {
	switch {
	case g.mergeID != "":
		return "merge"
	case g.ownership:
		return "ownership"
	case g.reason != "":
		return "judge"
	case g.cmd.Cmd == "roster-add":
		return "roster"
	default:
		return "approve"
	}
}

// metricsSnapshot copies the live state the endpoint renders; a tombstoned role reads as exited.
func (s *session) metricsSnapshot(now time.Time) *metricsSnapshot {
	snap := &metricsSnapshot{gatewayOn: s.sphragisOn, gatewayUp: s.gatewayUp, priced: len(s.cfg.Pricing) > 0,
		gates: map[string]int{}, counters: s.metrics.clone(), usage: maps.Clone(s.lastUsage)}
	seen := map[string]int{}
	for _, e := range s.panes {
		rm := roleMetrics{name: e.role.Name, state: paneState(e, now), budget: e.role.BudgetUSD(), overBudget: e.overBudget}
		if e.gone {
			rm.state = "exited"
		}
		if i, ok := seen[rm.name]; ok {
			if !e.gone {
				snap.roles[i] = rm // a live role wins over its tombstone of the same name
			}
			continue
		}
		seen[rm.name] = len(snap.roles)
		snap.roles = append(snap.roles, rm)
	}
	slices.SortFunc(snap.roles, func(a, b roleMetrics) int { return cmp.Compare(a.name, b.name) })
	for _, g := range s.gates {
		snap.gates[gateKind(g)]++
	}
	for _, n := range snap.counters.tasks {
		snap.openTasks += n
	}
	for _, n := range snap.counters.done {
		snap.openTasks -= n
	}
	return snap
}

// metricsServer serves GET /metrics from the last published snapshot.
type metricsServer struct {
	version, mode string
	started       time.Time
	addr          string
	snap          atomic.Pointer[metricsSnapshot]
	srv           *http.Server
}

// startMetrics opens the [metrics] listener; a bind failure warns and leaves the deck without metrics.
func (s *session) startMetrics() {
	listen := s.cfg.Metrics.Listen
	if listen == "" {
		return
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		s.log().Warn("metrics listen failed; running without metrics", "addr", listen, "err", err)
		return
	}
	ms := &metricsServer{version: buildVersion, mode: cmp.Or(s.mode, "tui"), started: time.Now(), addr: ln.Addr().String()}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", ms)
	ms.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = ms.srv.Serve(ln) }()
	s.metricsSrv = ms
	s.log().Info("metrics ready", "addr", ms.addr)
}

// stopMetrics closes the listener; idempotent.
func (s *session) stopMetrics() {
	if s.metricsSrv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.metricsSrv.srv.Shutdown(ctx)
	s.metricsSrv = nil
}

// metricsAddr is the bound address, empty when not serving.
func (s *session) metricsAddr() string {
	if s.metricsSrv == nil {
		return ""
	}
	return s.metricsSrv.addr
}

// publishMetrics refreshes the snapshot the handler renders; both loops call it on their tick.
func (s *session) publishMetrics() {
	if s.metricsSrv == nil {
		return
	}
	s.metricsSrv.snap.Store(s.metricsSnapshot(time.Now()))
}

func (ms *metricsServer) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	ms.render(w, ms.snap.Load())
}

// render writes the exposition text; a nil snapshot (before the first tick) carries the header series only.
func (ms *metricsServer) render(w io.Writer, snap *metricsSnapshot) {
	var b bytes.Buffer
	header(&b, "choragos_info", "gauge", "Build version and run mode of the deck.")
	sample(&b, "choragos_info", 1, "version", ms.version, "mode", ms.mode)
	header(&b, "choragos_session_start_time_seconds", "gauge", "Unix time the deck started.")
	sample(&b, "choragos_session_start_time_seconds", float64(ms.started.Unix()))
	if snap == nil {
		_, _ = w.Write(b.Bytes())
		return
	}
	if snap.gatewayOn {
		header(&b, "choragos_gateway_up", "gauge", "Whether the Sphragis gateway answered its last health probe.")
		sample(&b, "choragos_gateway_up", b2f(snap.gatewayUp))
	}
	c := snap.counters
	header(&b, "choragos_role_state", "gauge", "Role state, one-hot across working, waiting, idle, paused and exited.")
	for _, r := range snap.roles {
		for _, st := range metricStates {
			sample(&b, "choragos_role_state", b2f(r.state == st), "role", r.name, "state", st)
		}
	}
	for _, m := range []struct {
		name, help string
		vals       map[string]int
	}{
		{"choragos_role_tasks_total", "Delegations handed to the role, judge rounds included.", c.tasks},
		{"choragos_role_tasks_done_total", "Delegations the role reported done.", c.done},
		{"choragos_role_tasks_timed_out_total", "Delegations that outlived the role's timeout.", c.timedOut},
		{"choragos_role_restarts_total", "Auto and manual restarts of the role's process.", c.restarts},
	} {
		header(&b, m.name, "counter", m.help)
		for _, r := range snap.roles {
			sample(&b, m.name, float64(m.vals[r.name]), "role", r.name)
		}
	}
	header(&b, "choragos_role_busy_seconds_total", "counter", "Seconds between delegate and work-done, summed over done tasks.")
	for _, r := range snap.roles {
		sample(&b, "choragos_role_busy_seconds_total", c.busy[r.name].Seconds(), "role", r.name)
	}
	if len(snap.usage) > 0 {
		header(&b, "choragos_role_tokens_total", "counter", "Tokens the gateway counted for the role, by direction.")
		for _, r := range snap.roles {
			u, ok := snap.usage[r.name]
			if !ok {
				continue
			}
			for _, d := range directions {
				sample(&b, "choragos_role_tokens_total", float64(tokensFor(u, d)), "role", r.name, "direction", d)
			}
		}
		if snap.priced {
			header(&b, "choragos_role_cost_usd", "gauge", "Session cost of the role from the [pricing] table.")
			for _, r := range snap.roles {
				if u, ok := snap.usage[r.name]; ok {
					sample(&b, "choragos_role_cost_usd", u.Cost, "role", r.name)
				}
			}
		}
	}
	header(&b, "choragos_role_budget_usd", "gauge", "Configured budget of the role; absent when unset.")
	for _, r := range snap.roles {
		if r.budget > 0 {
			sample(&b, "choragos_role_budget_usd", r.budget, "role", r.name)
		}
	}
	header(&b, "choragos_role_over_budget", "gauge", "Whether the role's budget action has fired.")
	for _, r := range snap.roles {
		sample(&b, "choragos_role_over_budget", b2f(r.overBudget), "role", r.name)
	}
	for _, m := range []struct {
		name, label, help string
		vals              map[labelKey]int
	}{
		{"choragos_judge_rounds_total", "verdict", "Judge verdicts by outcome for the builder role.", c.judge},
		{"choragos_check_runs_total", "verdict", "Check command runs by outcome for the builder role.", c.check},
		{"choragos_merges_total", "outcome", "Worktree merge outcomes for the role.", c.merges},
	} {
		if len(m.vals) == 0 {
			continue
		}
		header(&b, m.name, "counter", m.help)
		for _, k := range sortedKeys(m.vals) {
			sample(&b, m.name, float64(m.vals[k]), "role", k.role, m.label, k.label)
		}
	}
	if len(c.score) > 0 {
		header(&b, "choragos_judge_score", "gauge", "Last valid judge score for the builder role, out of 10.")
		for _, role := range slices.Sorted(maps.Keys(c.score)) {
			sample(&b, "choragos_judge_score", float64(c.score[role]), "role", role)
		}
	}
	header(&b, "choragos_tasks_open", "gauge", "Delegations without a work-done yet.")
	sample(&b, "choragos_tasks_open", float64(snap.openTasks))
	header(&b, "choragos_gates_pending", "gauge", "Gates waiting for a human, by kind.")
	for _, k := range gateKinds {
		sample(&b, "choragos_gates_pending", float64(snap.gates[k]), "kind", k)
	}
	_, _ = w.Write(b.Bytes())
}

func tokensFor(u roleUsage, direction string) int64 {
	switch direction {
	case "input":
		return u.In
	case "output":
		return u.Out
	case "cache_creation":
		return u.CacheCreation
	default:
		return u.CacheRead
	}
}

func sortedKeys(m map[labelKey]int) []labelKey {
	keys := slices.Collect(maps.Keys(m))
	slices.SortFunc(keys, func(a, b labelKey) int { return cmp.Or(cmp.Compare(a.role, b.role), cmp.Compare(a.label, b.label)) })
	return keys
}

func b2f(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func header(b *bytes.Buffer, name, typ, help string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

// sample writes one line; kv are label name/value pairs, values escaped per the exposition format.
func sample(b *bytes.Buffer, name string, v float64, kv ...string) {
	b.WriteString(name)
	if len(kv) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(kv); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(b, `%s="%s"`, kv[i], labelEscaper.Replace(kv[i+1]))
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
	b.WriteByte('\n')
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
