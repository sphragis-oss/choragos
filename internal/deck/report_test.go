// SPDX-License-Identifier: Apache-2.0

package deck

import (
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
