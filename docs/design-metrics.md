# Design: live metrics endpoint

An opt-in Prometheus text endpoint served by the running deck: role
state, task counters, gate backlog, judge and check outcomes, token
burn and cost per role, all readable while the session runs. Today the
only live view is the deck itself, and the only machine-readable one
is `choragos report --json` after the fact, from `events.log`. A
`[metrics]` table gives an unattended run a scrape target for
Prometheus, Grafana Alloy, the OTel Collector, or the Datadog agent,
with no new dependency and no change to any flow when unconfigured.

Status: implemented as designed (`internal/deck/metrics.go`), with one
delta: in TUI mode the token series follow the sidebar's 1s usage
fetch rather than the 30s snapshot, since the loop already holds that
data; server mode is paced at 30s as written below.

## Why an endpoint, not a push

The deck already speaks this format from the other side: token counts
come from scraping the gateway's `/metrics` with a regex
(`internal/deck/usage.go`), and the per-role usage it keeps is a set
of cumulative counters. Serving the same shape back out is a relabel
of state the deck holds anyway.

The alternatives cost more than they give. An OTLP exporter brings
the OpenTelemetry SDK plus gRPC and protobuf into a binary whose
direct dependencies fit on one screen, for a collector that can scrape
Prometheus natively. `prometheus/client_golang` brings six modules to
write `# TYPE` lines that `fmt.Fprintf` writes in the same number of
lines. The text exposition format is a few dozen lines of Go with
`net/http`, symmetric with the scraper, and every consumer listed
above reads it. OTLP stays reachable through the collector's
Prometheus receiver and is a non-goal here.

## Configuration

```toml
[metrics]
listen = "127.0.0.1:9464"   # off when absent
```

- `listen` is a `host:port` handed to `net.Listen("tcp", ...)`. Absent
  or empty means no listener, no goroutine, no new code path: today's
  deck exactly. Port `0` picks a free port; the bound address is logged
  (`metrics ready addr=...`) and written to the session's meta file
  so `choragos ls` can show it.
- The endpoint is `GET /metrics`. Anything else is 404.
- A bind failure (port taken, address invalid) is a startup warning
  in `events.log`, and the deck runs without metrics. Metrics are
  observability, never a reason to refuse a session; this mirrors
  `sphragis auto-off`, and `doctor` is the pre-flight that catches it.
- `doctor` prints one line: `OK metrics: 127.0.0.1:9464 bindable`,
  `WARN metrics: ... in use` (it tries a bind and closes it), or
  `WARN metrics: listen on a non-loopback address exposes the
  endpoint without auth`. Absent key: `OK metrics: off`.

`listen` is a config-file key only; `roster-add` cannot set it. The
table reloads like the rest: a changed `listen` on `choragos reload`
closes the old listener and opens the new one, a removed table closes
it.

## What is exported

Names mirror the `report --json` schema (`internal/deck/report.go`,
`jsonRole`) so a dashboard built on the live numbers reconciles with
the post-run report. Labels are role names and small fixed enums;
task text, ids, briefs and paths are never exported, so the endpoint
carries no more than the sidebar shows.

| Metric | Type | Labels | Source |
|--------|------|--------|--------|
| `choragos_info` | gauge, always 1 | `version`, `mode` (`tui`, `server`) | the `deck starting` header |
| `choragos_session_start_time_seconds` | gauge | | session start |
| `choragos_gateway_up` | gauge 0/1 | | `gatewayUp`; absent when the gateway is off |
| `choragos_role_state` | gauge, one-hot | `role`, `state` (`working`, `waiting`, `idle`, `paused`, `exited`) | `computeStatus` |
| `choragos_role_tasks_total` | counter | `role` | every `delegate` to the role, deck-synthesized rounds included |
| `choragos_role_tasks_done_total` | counter | `role` | every `work-done` from the role |
| `choragos_role_tasks_timed_out_total` | counter | `role` | `delegate timeout` |
| `choragos_role_busy_seconds_total` | counter | `role` | delegate-to-work-done span, added at work-done |
| `choragos_role_restarts_total` | counter | `role` | auto and manual restarts, fresh respawns excluded |
| `choragos_role_tokens_total` | counter | `role`, `direction` (`input`, `output`, `cache_creation`, `cache_read`) | last gateway snapshot; absent without the gateway |
| `choragos_role_cost_usd` | gauge | `role` | same snapshot; absent without `[pricing]` |
| `choragos_role_budget_usd` | gauge | `role` | `budget`; absent when unset |
| `choragos_role_over_budget` | gauge 0/1 | `role` | `overBudget` latch |
| `choragos_judge_rounds_total` | counter | `role`, `verdict` (`pass`, `fail`, `invalid`) | `judge` lines |
| `choragos_judge_score` | gauge | `role` | last valid verdict for the builder role |
| `choragos_check_runs_total` | counter | `role`, `verdict` (`pass`, `fail`, `unavailable`) | `check` lines |
| `choragos_merges_total` | counter | `role`, `outcome` (`merged`, `failed`, `kept`) | worktree merge outcomes |
| `choragos_tasks_open` | gauge | | delegations without a work-done |
| `choragos_gates_pending` | gauge | `kind` (`approve`, `judge`, `ownership`, `merge`, `roster`) | `s.gates` classified by its flags |

`busy_seconds_total` over `tasks_done_total` is the report's average
task time; a histogram is not needed for five roles. `rate()` over
`tokens_total` is the burn the sidebar cannot show.

Counters are kept on the session as a small struct incremented at the
same call sites that write the `events.log` line, not derived from the
task board: the board is capped at 200 entries (`boardCap`) and would
silently undercount a long run. Gauges are read from live state at
snapshot time.

## Reading state off the loop thread

The deck's core is single-threaded by construction: every field on
`session` mutates inside the Bubble Tea update loop or the server's
message loop, and there is no mutex to take. An HTTP handler runs on
its own goroutine and must not touch `session`.

The loop therefore publishes, and the handler only reads:

- On every tick (the existing 1s ticker in both loops) the loop builds
  a `metricsSnapshot`: a plain struct of the counters, per-role
  gauges, gate counts and the last usage map, with no pointers into
  live state. It stores it in an `atomic.Pointer[metricsSnapshot]` on
  the session.
- `GET /metrics` loads the pointer and renders text from the copy.
  A scrape never blocks the loop, and the loop never waits on a
  scrape. A snapshot is at most one tick stale, which is finer than
  any scrape interval.
- Before the first tick the handler serves the `_info` and start-time
  lines only, so a scraper that arrives during boot sees a valid page.

Token usage reaches the loop today as `budgetMsg`, a cost-only map
built by the 30s `logTokens` snapshot. The snapshot keeps the parsed
`roleUsage` map on the session as well (`lastUsage`), which is the
same data the TUI already holds in `Model.usage`; server mode gains
it for free. Tokens on the endpoint are therefore paced at 30s in both
modes, matching `events.log`.

Rendering is `fmt.Fprintf` of `# HELP`, `# TYPE` and sample lines,
with label values escaped per the exposition format (backslash, quote,
newline). Role names are config identifiers and never contain any of
the three, but the escaper runs regardless.

## Lifecycle

- Listener opened in `session.start` after the control socket, bound
  address logged and stored in `ipc.Meta` (new optional `metrics`
  field, additive). `http.Server` with `ReadHeaderTimeout` set;
  no keep-alive tuning needed for a scraper.
- Closed in `closeAll` before the sockets, with a short shutdown
  deadline. `serve --detach` keeps it with the session; `attach`
  clients never serve metrics, the server does.
- `reload` diffs `listen` and reopens when it changed.

## Failure modes

| Failure | Behavior |
|---------|----------|
| No `[metrics]` table | Nothing above exists; today's deck exactly |
| Port in use or bad address at start | Warning in `events.log`; session runs without metrics |
| Port in use on reload | Old listener already closed; warning, metrics off until the next reload |
| Non-loopback `listen` | Served as asked; `doctor` warns, the docs say why |
| Scrape during boot | Valid page with `_info` and start time only |
| Scrape after `closeAll` began | Connection refused; the scraper's `up` metric says so |
| Gateway off or `[pricing]` unset | Token and cost series absent, never zero, so a dashboard cannot mistake "unmeasured" for "free" |
| Role removed by reload | Its series stay until the session ends: counters must not vanish mid-run, and the one-hot state reports `exited` |

## Non-goals (v1)

- OTLP push, or any exporter beyond the text endpoint.
- Authentication or TLS on the endpoint; bind to loopback and let a
  local collector forward, as the sandboxing recipes do for egress.
- Per-task labels (`id`, `task`): unbounded cardinality and task text
  on a network port.
- Duration histograms; counters plus `rate()` answer the questions a
  five-role team asks.
- Serving metrics from `attach` clients or the desktop app.
- Persisting counters across `--resume`: a resumed session starts its
  counters at zero, and the report remains the cross-restart view.

## Staging

1. `[metrics]` config table, load validation (empty or absent means
   off), doctor line, docs/configuration.md section, unit tests.
2. Counter struct and increments at the existing log call sites,
   `lastUsage` on the session, snapshot on tick in both loops,
   listener lifecycle in start, reload and closeAll, `ipc.Meta` field.
   Handler and renderer with an `httptest` test that parses every
   sample line against the exposition grammar and checks label
   escaping, absent-series rules, and the one-hot state.
3. `scripts/e2e-smoke.sh` curls the endpoint on the demo team and
   asserts one `choragos_role_state` line per role; CHANGELOG.
