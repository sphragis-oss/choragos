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
