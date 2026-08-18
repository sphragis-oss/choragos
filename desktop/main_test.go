// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDesktopLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	f := openDesktopLog(dir)
	if f == nil {
		t.Fatal("openDesktopLog returned nil")
	}
	if _, err := f.WriteString("first\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// append mode keeps history across launches
	f = openDesktopLog(dir)
	if f == nil {
		t.Fatal("reopen returned nil")
	}
	_ = f.Close()
	data, err := os.ReadFile(filepath.Join(dir, "desktop.log"))
	if err != nil || string(data) != "first\n" {
		t.Fatalf("desktop.log = %q err=%v", data, err)
	}

	// an oversized file rotates aside on the next launch
	if err := os.WriteFile(filepath.Join(dir, "desktop.log"), make([]byte, desktopLogMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	f = openDesktopLog(dir)
	if f == nil {
		t.Fatal("post-rotate open returned nil")
	}
	_ = f.Close()
	if _, err := os.Stat(filepath.Join(dir, "desktop.log.1")); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "desktop.log"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 0 {
		t.Fatalf("fresh desktop.log after rotation has size %d", fi.Size())
	}
}
