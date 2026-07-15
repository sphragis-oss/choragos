// SPDX-License-Identifier: Apache-2.0

package deck

import (
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

// writeReport parses event lines and renders the per-role table.
func writeReport(w io.Writer, log, path string) error {
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
		return fmt.Errorf("no events found in %s", path)
	}
	fmt.Fprintf(w, "run %s · wall %s", start.Format("2006-01-02 15:04:05"), end.Sub(start).Round(time.Second))
	if dir != "" {
		fmt.Fprintf(w, " · %s", dir)
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ROLE\tTASKS\tDONE\tBUSY\tAVG\tFIRST\tLAST\tTOKENS")
	open := 0
	for _, name := range order {
		r := rows[name]
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
