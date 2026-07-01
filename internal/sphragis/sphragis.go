// SPDX-License-Identifier: Apache-2.0

// Package sphragis supervises the local Sphragis gateway that agent traffic routes through.
package sphragis

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
)

// Healthy reports whether a gateway answers on addr, probing its local /metrics endpoint.
func Healthy(addr string) bool {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/metrics")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Supervisor tracks a gateway; Close stops it only if choragos started it.
type Supervisor struct {
	addr    string
	cmd     *exec.Cmd
	managed bool
}

// Ensure returns a healthy gateway on cfg.Addr, spawning "cfg.Command serve" if none is listening.
func Ensure(cfg config.Sphragis) (*Supervisor, error) {
	if Healthy(cfg.Addr) {
		return &Supervisor{addr: cfg.Addr}, nil // already running, not ours to stop
	}
	cmd := exec.Command(cfg.Command, "serve")
	cmd.Env = append(os.Environ(), "SPHRAGIS_LISTEN_ADDR="+listenAddr(cfg.Addr))
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Command, err)
	}
	for i := 0; i < 40; i++ {
		if Healthy(cfg.Addr) {
			return &Supervisor{addr: cfg.Addr, cmd: cmd, managed: true}, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return nil, fmt.Errorf("gateway did not become healthy on %s", cfg.Addr)
}

// Healthy reports whether this supervisor's gateway is currently up.
func (s *Supervisor) Healthy() bool { return s != nil && Healthy(s.addr) }

// Close stops the gateway if choragos started it.
func (s *Supervisor) Close() error {
	if s == nil || !s.managed || s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	err := s.cmd.Process.Kill()
	_ = s.cmd.Wait()
	return err
}

// listenAddr turns a host:port into the ":port" form sphragis expects.
func listenAddr(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return ":" + port
	}
	return addr
}
