package session

import (
	"testing"
	"time"
)

func TestReferenceAndExpiry(t *testing.T) {
	m := New(5*time.Second, 2*time.Second)
	n := time.Now()
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		s, first := m.Open(id, "SIM-001", "u", n)
		if first != (i == 0) || s.TelemetryHz != 1 {
			t.Fatal("duplicate upstream")
		}
	}
	if m.Close("a", "other", n) {
		t.Fatal("cross-user close")
	}
	if _, ok := m.Renew("a", "u", n.Add(time.Second)); !ok {
		t.Fatal("renew")
	}
	if len(m.Sweep(n.Add(7*time.Second))) != 0 {
		t.Fatal("grace")
	}
	if len(m.Sweep(n.Add(8*time.Second))) != 1 || m.Count() != 0 {
		t.Fatal("expiry")
	}
}
