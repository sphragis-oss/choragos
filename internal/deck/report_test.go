// SPDX-License-Identifier: Apache-2.0

package deck

import (
	"encoding/json"
	"strings"
	"testing"
)

const sampleEvents = `time=2026-07-13T13:13:35.634+03:00 level=INFO msg="deck starting" roles=3 sphragis=true dir=/tmp/demo
time=2026-07-13T13:13:37.634+03:00 level=INFO msg=boot role=orchestrator start=true
time=2026-07-13T13:13:39.636+03:00 level=INFO msg=boot role=coder start=false
time=2026-07-13T13:13:40.000+03:00 level=INFO msg=delegate id=T1 from=orchestrator to=coder task="fix the \"thing\"" brief=""
time=2026-07-13T13:14:40.000+03:00 level=INFO msg=work-done id=T1 to=orchestrator done=true task="fixed" report=""
time=2026-07-13T13:15:00.000+03:00 level=INFO msg=delegate id=T2 from=orchestrator to=coder task="second" brief=""
time=2026-07-13T13:15:10.000+03:00 level=INFO msg=tokens role=coder in=1500 out=200 cache_creation=0 cache_read=500000
time=2026-07-13T13:16:00.000+03:00 level=INFO msg=tokens role=coder in=2000 out=300 cache_creation=0 cache_read=900000
time=2026-07-13T13:16:05.634+03:00 level=INFO msg="deck stopping"
`

func TestWriteReport(t *testing.T) {
	var sb strings.Builder
	if err := writeReport(&sb, sampleEvents, "events.log"); err != nil {
		t.Fatal(err)
	}
	got := sb.String()
	for _, want := range []string{
		"run 2026-07-13 13:13:35 · wall 2m30s · /tmp/demo",
		"coder", "1m0s", // one completed task, 60s busy
		"↑902k ↓300", // last cumulative snapshot wins: 2000+900000 up
		"n/a",        // orchestrator never got a token snapshot
		"1 task(s) never reported work-done",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	// coder row: 2 delegated, 1 done
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "coder") {
			f := strings.Fields(line)
			if f[1] != "2" || f[2] != "1" {
				t.Errorf("coder tasks/done = %s/%s, want 2/1", f[1], f[2])
			}
		}
	}
}

func TestWriteReportEmpty(t *testing.T) {
	if err := writeReport(&strings.Builder{}, "", "x.log"); err == nil {
		t.Fatal("empty log should error")
	}
}

func TestWriteReportJSON(t *testing.T) {
	var sb strings.Builder
	if err := writeReportJSON(&sb, sampleEvents, "events.log"); err != nil {
		t.Fatal(err)
	}
	var got jsonReport
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, sb.String())
	}
	if got.WallSeconds != 150 || got.OpenTasks != 1 || got.Dir == nil || *got.Dir != "/tmp/demo" {
		t.Fatalf("header = wall %v open %d dir %v", got.WallSeconds, got.OpenTasks, got.Dir)
	}
	byRole := map[string]jsonRole{}
	for _, r := range got.Roles {
		byRole[r.Role] = r
	}
	coder := byRole["coder"]
	if coder.Tasks != 2 || coder.Done != 1 {
		t.Fatalf("coder tasks/done = %d/%d, want 2/1", coder.Tasks, coder.Done)
	}
	if coder.BusySeconds == nil || *coder.BusySeconds != 60 || coder.AvgSeconds == nil || *coder.AvgSeconds != 60 {
		t.Fatalf("coder busy/avg = %v/%v, want 60/60", coder.BusySeconds, coder.AvgSeconds)
	}
	// last cumulative snapshot wins
	if coder.Tokens == nil || coder.Tokens.In != 2000 || coder.Tokens.Out != 300 || coder.Tokens.CacheRead != 900000 {
		t.Fatalf("coder tokens = %+v", coder.Tokens)
	}
	orch := byRole["orchestrator"]
	if orch.Tokens != nil {
		t.Fatal("gateway-less role must have null tokens")
	}
	if orch.First == nil || orch.Last == nil {
		t.Fatal("active role must carry first/last timestamps")
	}
	// no completions: busy and avg are explicit nulls in the document
	if orch.BusySeconds != nil || !strings.Contains(sb.String(), `"busy_seconds": null`) {
		t.Fatalf("busy_seconds must be an explicit null:\n%s", sb.String())
	}
	if err := writeReportJSON(&strings.Builder{}, "", "x.log"); err == nil {
		t.Fatal("empty log should error")
	}
}

func TestWriteReportBudget(t *testing.T) {
	log := sampleEvents +
		`time=2026-07-13T13:16:01.000+03:00 level=INFO msg=tokens role=coder in=2000 out=300 cache_creation=0 cache_read=900000 cost=5.1000
time=2026-07-13T13:16:02.000+03:00 level=WARN msg="budget exceeded" role=coder budget=5.00 cost=5.10 action=notify
`
	var sb strings.Builder
	if err := writeReport(&sb, log, "events.log"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"COST", "$5.10", "budget exceeded: coder ($5.00)"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("report missing %q:\n%s", want, sb.String())
		}
	}

	sb.Reset()
	if err := writeReportJSON(&sb, log, "events.log"); err != nil {
		t.Fatal(err)
	}
	var got jsonReport
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatal(err)
	}
	byRole := map[string]jsonRole{}
	for _, r := range got.Roles {
		byRole[r.Role] = r
	}
	coder := byRole["coder"]
	if coder.CostUSD == nil || *coder.CostUSD != 5.1 || coder.BudgetUSD == nil || *coder.BudgetUSD != 5 {
		t.Fatalf("coder cost/budget = %v/%v, want 5.1/5", coder.CostUSD, coder.BudgetUSD)
	}
	orch := byRole["orchestrator"]
	if orch.CostUSD != nil || orch.BudgetUSD != nil {
		t.Fatal("unpriced role must have null cost and budget")
	}
}

func TestParseLogfmt(t *testing.T) {
	kv := parseLogfmt(`time=2026-07-13T13:13:40.000+03:00 level=INFO msg=delegate id=T1 to=coder task="fix the \"thing\"" brief=""`)
	cases := map[string]string{
		"msg":   "delegate",
		"to":    "coder",
		"task":  `fix the "thing"`,
		"brief": "",
	}
	for k, want := range cases {
		if kv[k] != want {
			t.Errorf("kv[%q] = %q, want %q", k, kv[k], want)
		}
	}
	if kv := parseLogfmt(`msg="unterminated quote`); kv["msg"] != "" {
		t.Errorf("unterminated quote should stop parsing, got %q", kv["msg"])
	}
}
