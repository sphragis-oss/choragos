// SPDX-License-Identifier: Apache-2.0

package sphragis_test

import (
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/sphragis"
)

// TestMain doubles as a fake gateway: Ensure re-execs this binary as "<cmd> serve".
func TestMain(m *testing.M) {
	if os.Getenv("SPHRAGIS_TEST_HELPER") == "1" {
		addr := os.Getenv("SPHRAGIS_LISTEN_ADDR")
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		if err := http.ListenAndServe(addr, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestEnsureSpawnsAndCloseStops(t *testing.T) {
	addr := freeAddr(t)
	t.Setenv("SPHRAGIS_TEST_HELPER", "1")
	sup, err := sphragis.Ensure(config.Sphragis{Command: os.Args[0], Addr: addr})
	if err != nil {
		t.Fatalf("Ensure should spawn and adopt the gateway: %v", err)
	}
	if !sup.Healthy() {
		t.Error("spawned gateway should report healthy")
	}
	if err := sup.Close(); err != nil {
		t.Fatalf("Close on a managed gateway: %v", err)
	}
	for i := 0; i < 40 && sphragis.Healthy(addr); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if sphragis.Healthy(addr) {
		t.Error("gateway still answering after Close; managed process must be stopped")
	}
}

func TestEnsureFailsClosedWhenSpawnFails(t *testing.T) {
	_, err := sphragis.Ensure(config.Sphragis{Command: "choragos-no-such-binary", Addr: "127.0.0.1:1"})
	if err == nil {
		t.Fatal("Ensure with a missing binary and dead addr must fail closed")
	}
	if !strings.Contains(err.Error(), "start choragos-no-such-binary") {
		t.Errorf("error should name the command it failed to start, got: %v", err)
	}
}

func TestNilSupervisor(t *testing.T) {
	var s *sphragis.Supervisor
	if s.Healthy() {
		t.Error("nil supervisor must not report healthy")
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil supervisor Close: %v", err)
	}
}
