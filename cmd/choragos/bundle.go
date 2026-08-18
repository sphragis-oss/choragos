// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
)

// coreLogs are always bundled; everything else under logs/ is a role transcript.
var coreLogs = map[string]bool{"events.log": true, "events.log.1": true, "server.log": true, "crash.log": true}

// bundlePath resolves the --bundle value; "auto" becomes a timestamped name.
func bundlePath(v string) string {
	if v == "auto" {
		return fmt.Sprintf("choragos-debug-%s.tar.gz", time.Now().Format("20060102-150405"))
	}
	return v
}

// writeBundle tars the debug evidence: meta, doctor output, config, session state, logs.
// Missing files are skipped (a bundle from a half-broken workspace is the point);
// write errors are sticky in tar/gzip and surface at close.
func writeBundle(path, cfgPath string, doctor []byte, transcripts bool) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("bundle: %w", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now()})
		_, _ = tw.Write(data)
	}
	wd, _ := os.Getwd()
	meta := fmt.Sprintf("version=%s\nos=%s/%s\ngo=%s\ntime=%s\ndir=%s\n",
		version, runtime.GOOS, runtime.GOARCH, runtime.Version(), time.Now().Format(time.RFC3339), wd)
	add("meta.txt", []byte(meta))
	add("doctor.txt", doctor)
	addFile := func(rel string) {
		if data, err := os.ReadFile(rel); err == nil {
			add(filepath.ToSlash(rel), data)
		}
	}
	if cfgPath == "" {
		cfgPath = config.DefaultFile
	}
	addFile(cfgPath)
	addFile(filepath.Join(".choragos", "session.json"))
	logs := filepath.Join(".choragos", "logs")
	entries, _ := os.ReadDir(logs)
	for _, e := range entries {
		if e.IsDir() || (!transcripts && !coreLogs[e.Name()]) {
			continue
		}
		addFile(filepath.Join(logs, e.Name()))
	}
	if err := errors.Join(tw.Close(), gz.Close(), f.Close()); err != nil {
		return fmt.Errorf("bundle: %w", err)
	}
	return nil
}
