// SPDX-License-Identifier: Apache-2.0

package sphragis_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sphragis-oss/choragos/internal/config"
	"github.com/sphragis-oss/choragos/internal/sphragis"
)

func TestHealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	if !sphragis.Healthy(addr) {
		t.Error("expected healthy for a live /metrics server")
	}
	if sphragis.Healthy("127.0.0.1:1") {
		t.Error("expected unhealthy for a closed port")
	}
}

func TestAutoOff(t *testing.T) {
	yes, no := true, false
	missing := config.Sphragis{Command: "choragos-no-such-binary", Addr: "127.0.0.1:1"}
	if !sphragis.AutoOff(missing) {
		t.Error("implicit default with missing binary and dead addr should auto-off")
	}
	explicit := missing
	explicit.Enabled = &yes
	if sphragis.AutoOff(explicit) {
		t.Error("explicit enabled = true must never auto-off")
	}
	disabled := missing
	disabled.Enabled = &no
	if sphragis.AutoOff(disabled) {
		t.Error("explicit enabled = false is not auto-off's call")
	}
	inPath := missing
	inPath.Command = "sh"
	if sphragis.AutoOff(inPath) {
		t.Error("binary in PATH should keep the default on")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	running := missing
	running.Addr = strings.TrimPrefix(ts.URL, "http://")
	if sphragis.AutoOff(running) {
		t.Error("a healthy gateway on addr should keep the default on")
	}
}

func TestEnsureAttachesRunningGateway(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	sup, err := sphragis.Ensure(config.Sphragis{Addr: addr, Command: "sphragis"})
	if err != nil {
		t.Fatalf("Ensure with a healthy gateway: %v", err)
	}
	if !sup.Healthy() {
		t.Error("supervisor should report healthy")
	}
	// gateway was already running, so Close must not try to kill it
	if err := sup.Close(); err != nil {
		t.Errorf("Close on unmanaged gateway: %v", err)
	}
}
