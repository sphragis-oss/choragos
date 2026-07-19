// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// reportRow aggregates one role's activity across a run's event log.
type reportRow struct {
	role        string
	tasks, done int
	busy        time.Duration
	first, last time.Time
	usage       roleUsage
	hasTokens   bool
}

// Report summarizes an events.log into a per-role activity table on w.
func Report(path string, w io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read event log: %w", err)
	}
	return writeReport(w, string(data), path)
}

// ReportJSON emits the same summary as a stable JSON document on w.
func ReportJSON(path string, w io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read event log: %w", err)
	}
	return writeReportJSON(w, string(data), path)
}

// reportData is the aggregated run summary shared by the text and JSON renderers.
type reportData struct {
	start, end time.Time
	dir        string
	order      []string
	rows       map[string]*reportRow
}

// aggregateReport parses event lines into the per-role summary.
func aggregateReport(log, path string) (reportData, error) {
	rows := map[string]*reportRow{}
	var order []string
	row := func(name string) *reportRow {
		if r, ok := rows[name]; ok {
			return r
		}
		r := &reportRow{role: name}
		rows[name] = r
		order = append(order, name)
		return r
	}
	touch := func(r *reportRow, t time.Time) {
		if r.first.IsZero() || t.Before(r.first) {
			r.first = t
		}
		if t.After(r.last) {
			r.last = t
		}
	}
	type openTask struct {
		role string
		at   time.Time
	}
	pending := map[string]openTask{}
	var start, end time.Time
	var dir string
	for _, line := range strings.Split(log, "\n") {
		kv := parseLogfmt(line)
		t, err := time.Parse(time.RFC3339Nano, kv["time"])
		if err != nil {
			continue
		}
		if start.IsZero() {
			start = t
		}
		end = t
		switch kv["msg"] {
		case "deck starting":
			if dir == "" {
				dir = kv["dir"]
			}
		case "delegate":
			r := row(kv["to"])
			r.tasks++
			touch(r, t)
			if id := kv["id"]; id != "" {
				pending[id] = openTask{role: kv["to"], at: t}
			}
		case "work-done":
			if o, ok := pending[kv["id"]]; ok {
				delete(pending, kv["id"])
				r := row(o.role)
				r.done++
				r.busy += t.Sub(o.at)
				touch(r, t)
			}
		case "tokens":
			r := row(kv["role"])
			r.usage = roleUsage{
				In:            parseCount(kv["in"]),
				Out:           parseCount(kv["out"]),
				CacheCreation: parseCount(kv["cache_creation"]),
				CacheRead:     parseCount(kv["cache_read"]),
			}
			r.hasTokens = true
		default:
			if name := kv["role"]; name != "" {
				touch(row(name), t)
			}
		}
	}
	if start.IsZero() {
		return reportData{}, fmt.Errorf("no events found in %s", path)
	}
	return reportData{start: start, end: end, dir: dir, order: order, rows: rows}, nil
}

// writeReport renders the per-role table.
func writeReport(w io.Writer, log, path string) error {
	d, err := aggregateReport(log, path)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "run %s · wall %s", d.start.Format("2006-01-02 15:04:05"), d.end.Sub(d.start).Round(time.Second))
	if d.dir != "" {
		fmt.Fprintf(w, " · %s", d.dir)
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ROLE\tTASKS\tDONE\tBUSY\tAVG\tFIRST\tLAST\tTOKENS")
	open := 0
	for _, name := range d.order {
		r := d.rows[name]
		open += r.tasks - r.done
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			r.role, r.tasks, r.done, reportDur(r.busy, r.done > 0), reportAvg(r.busy, r.done),
			reportClock(r.first), reportClock(r.last), reportTokens(r))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if open > 0 {
		fmt.Fprintf(w, "%d task(s) never reported work-done\n", open)
	}
	return nil
}

// jsonReport is the stable schema of report --json; fields with no data are explicit nulls.
type jsonReport struct {
	Start       time.Time  `json:"start"`
	End         time.Time  `json:"end"`
	WallSeconds float64    `json:"wall_seconds"`
	Dir         *string    `json:"dir"`
	OpenTasks   int        `json:"open_tasks"`
	Roles       []jsonRole `json:"roles"`
}

type jsonRole struct {
	Role        string      `json:"role"`
	Tasks       int         `json:"tasks"`
	Done        int         `json:"done"`
	BusySeconds *float64    `json:"busy_seconds"`
	AvgSeconds  *float64    `json:"avg_seconds"`
	First       *time.Time  `json:"first"`
	Last        *time.Time  `json:"last"`
	Tokens      *jsonTokens `json:"tokens"`
}

type jsonTokens struct {
	In            int64 `json:"in"`
	Out           int64 `json:"out"`
	CacheCreation int64 `json:"cache_creation"`
	CacheRead     int64 `json:"cache_read"`
}

// writeReportJSON renders the summary as indented JSON.
func writeReportJSON(w io.Writer, log, path string) error {
	d, err := aggregateReport(log, path)
	if err != nil {
		return err
	}
	out := jsonReport{Start: d.start, End: d.end, WallSeconds: d.end.Sub(d.start).Seconds(), Roles: []jsonRole{}}
	if d.dir != "" {
		out.Dir = &d.dir
	}
	for _, name := range d.order {
		r := d.rows[name]
		out.OpenTasks += r.tasks - r.done
		jr := jsonRole{Role: r.role, Tasks: r.tasks, Done: r.done}
		if r.done > 0 {
			busy := r.busy.Seconds()
			avg := busy / float64(r.done)
			jr.BusySeconds, jr.AvgSeconds = &busy, &avg
		}
		if !r.first.IsZero() {
			first := r.first
			jr.First = &first
		}
		if !r.last.IsZero() {
			last := r.last
			jr.Last = &last
		}
		if r.hasTokens {
			jr.Tokens = &jsonTokens{In: r.usage.In, Out: r.usage.Out,
				CacheCreation: r.usage.CacheCreation, CacheRead: r.usage.CacheRead}
		}
		out.Roles = append(out.Roles, jr)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// parseLogfmt splits one slog text line into key=value pairs, unquoting quoted values.
func parseLogfmt(line string) map[string]string {
	out := map[string]string{}
	rest := strings.TrimSpace(line)
	for rest != "" {
		key, tail, ok := strings.Cut(rest, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \"") {
			break
		}
		var val string
		if strings.HasPrefix(tail, `"`) {
			val, tail, ok = cutQuoted(tail)
			if !ok {
				break
			}
		} else {
			val, tail, _ = strings.Cut(tail, " ")
		}
		out[key] = val
		rest = strings.TrimLeft(tail, " ")
	}
	return out
}

// cutQuoted unquotes the leading Go-quoted string and returns the remainder.
func cutQuoted(s string) (string, string, bool) {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			v, err := strconv.Unquote(s[:i+1])
			if err != nil {
				return "", "", false
			}
			return v, s[i+1:], true
		}
	}
	return "", "", false
}

func parseCount(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func reportDur(d time.Duration, has bool) string {
	if !has {
		return "-"
	}
	return d.Round(time.Second).String()
}

func reportAvg(d time.Duration, done int) string {
	if done == 0 {
		return "-"
	}
	return (d / time.Duration(done)).Round(time.Second).String()
}

func reportClock(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("15:04:05")
}

// reportTokens renders the last cumulative snapshot, or n/a when the gateway never reported.
func reportTokens(r *reportRow) string {
	if !r.hasTokens {
		return "n/a"
	}
	u := r.usage
	return "↑" + formatTokens(u.In+u.CacheCreation+u.CacheRead) + " ↓" + formatTokens(u.Out)
}
