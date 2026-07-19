// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
)

const initQueryHelperEnv = "CHORAGOS_TEST_INIT_QUERY_HELPER"

// Regression for #132: bubbletea's init() must not query the terminal (OSC 11) on a controlling tty.
func TestNoTerminalQueryAtInit(t *testing.T) {
	if os.Getenv(initQueryHelperEnv) == "1" {
		fmt.Println("helper ok")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run", "^TestNoTerminalQueryAtInit$")
	cmd.Env = append(os.Environ(), initQueryHelperEnv+"=1", "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close() }()
	var out bytes.Buffer
	deadline := time.Now().Add(10 * time.Second)
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		_ = ptmx.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := ptmx.Read(buf)
		out.Write(buf[:n])
		if err != nil && !os.IsTimeout(err) {
			break // EOF/EIO once the child exits
		}
		if bytes.Contains(out.Bytes(), []byte("helper ok")) {
			break
		}
	}
	_ = cmd.Wait()
	if bytes.Contains(out.Bytes(), []byte("\x1b]11;?")) {
		t.Fatalf("binary queried terminal background (OSC 11) at init; output: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("helper ok")) {
		t.Fatalf("helper never ran; output: %q", out.String())
	}
}
