// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
)

// tarNames reads a bundle back as name -> content.
func tarNames(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[hdr.Name] = string(data)
	}
}

func TestDoctorBundle(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join(".choragos", "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		config.DefaultFile:                               "[[roles]]\nname = \"orchestrator\"\ncommand = \"sh\"\nstart = true\n",
		filepath.Join(".choragos", "session.json"):       "{}",
		filepath.Join(".choragos", "logs", "events.log"): "msg=e",
		filepath.Join(".choragos", "logs", "coder.log"):  "transcript",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := doctorCmd()
	if err := cmd.Flags().Set("bundle", "b.tar.gz"); err != nil {
		t.Fatal(err)
	}
	out, _ := runCLI(t, cmd, nil) // check failures still bundle; the error is fine
	if !strings.Contains(out, "debug bundle written to b.tar.gz") || !strings.Contains(out, "transcripts excluded") {
		t.Fatalf("doctor --bundle output:\n%s", out)
	}
	got := tarNames(t, "b.tar.gz")
	for _, name := range []string{"meta.txt", "doctor.txt", config.DefaultFile, ".choragos/session.json", ".choragos/logs/events.log"} {
		if _, ok := got[name]; !ok {
			t.Errorf("bundle missing %s (has %v)", name, got)
		}
	}
	if _, ok := got[".choragos/logs/coder.log"]; ok {
		t.Error("transcript bundled without --transcripts")
	}
	if !strings.Contains(got["meta.txt"], "version=") || !strings.Contains(got["meta.txt"], "os=") {
		t.Errorf("meta.txt = %q", got["meta.txt"])
	}
	if !strings.Contains(got["doctor.txt"], "config") {
		t.Errorf("doctor.txt = %q", got["doctor.txt"])
	}

	cmd = doctorCmd()
	for k, v := range map[string]string{"bundle": "b2.tar.gz", "transcripts": "true"} {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	out, _ = runCLI(t, cmd, nil)
	if strings.Contains(out, "transcripts excluded") {
		t.Fatalf("--transcripts must drop the exclusion note:\n%s", out)
	}
	if got := tarNames(t, "b2.tar.gz"); got[".choragos/logs/coder.log"] != "transcript" {
		t.Errorf("bundle with --transcripts = %v", got)
	}
}

func TestDoctorBundleUnwritable(t *testing.T) {
	t.Chdir(t.TempDir())
	cmd := doctorCmd()
	if err := cmd.Flags().Set("bundle", filepath.Join("no-such-dir", "b.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, cmd, nil); err == nil || !strings.Contains(err.Error(), "bundle:") {
		t.Fatalf("unwritable bundle path must fail, got %v", err)
	}
}

func TestBundlePath(t *testing.T) {
	if got := bundlePath("auto"); !strings.HasPrefix(got, "choragos-debug-") || !strings.HasSuffix(got, ".tar.gz") {
		t.Errorf("bundlePath(auto) = %q", got)
	}
	if got := bundlePath("x.tar.gz"); got != "x.tar.gz" {
		t.Errorf("bundlePath(x.tar.gz) = %q", got)
	}
}
