// SPDX-License-Identifier: Apache-2.0

package wm

import (
	"reflect"
	"testing"
)

func TestMarshalRoundTrip(t *testing.T) {
	tr := New(0)
	tr.Split(true, 1)  // 0 | 1, focus 1
	tr.Split(false, 2) // 1 over 2, focus 2
	tr.AdjustRatio(true, 0.2)
	tr.FocusRole(1)

	got, err := Unmarshal(tr.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.VisibleRoles(), tr.VisibleRoles()) {
		t.Fatalf("roles = %v, want %v", got.VisibleRoles(), tr.VisibleRoles())
	}
	if got.FocusedRole() != 1 {
		t.Fatalf("focused = %d, want 1", got.FocusedRole())
	}
	if !reflect.DeepEqual(got.Layout(100, 40), tr.Layout(100, 40)) {
		t.Fatalf("layout drifted:\n%v\n%v", got.Layout(100, 40), tr.Layout(100, 40))
	}
}

func TestUnmarshalGarbage(t *testing.T) {
	if _, err := Unmarshal([]byte("{not json")); err == nil {
		t.Fatal("garbage must error")
	}
	tr, err := Unmarshal([]byte("{}")) // empty: degrade to a single tile, never nil
	if err != nil || tr.FocusedRole() != 0 || len(tr.VisibleRoles()) != 1 {
		t.Fatalf("empty blob: tr=%+v err=%v", tr, err)
	}
	// a split missing one child degrades to a leaf instead of crashing Layout
	tr, err = Unmarshal([]byte(`{"root":{"leaf":false,"a":{"leaf":true,"role":3}},"focused":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Layout(80, 24); len(got) != 1 {
		t.Fatalf("degraded layout = %v", got)
	}
}
