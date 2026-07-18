// SPDX-License-Identifier: Apache-2.0

package sphragis

import "testing"

func TestListenAddr(t *testing.T) {
	if got := listenAddr("127.0.0.1:8080"); got != ":8080" {
		t.Errorf("listenAddr(host:port) = %q, want :8080", got)
	}
	if got := listenAddr("not-an-addr"); got != "not-an-addr" {
		t.Errorf("listenAddr(fallback) = %q, want the input unchanged", got)
	}
}
