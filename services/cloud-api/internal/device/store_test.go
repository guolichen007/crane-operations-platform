package device

import (
	"fmt"
	"testing"
)

func TestAutoProvisionIsLimitedToRetainedCountFixtures(t *testing.T) {
	for i := 1; i <= 20; i++ {
		if source, ok := legacySource(fmt.Sprintf("SIM-%03d", i)); !ok || source != "SIMULATION" {
			t.Fatal("original count fixture rejected", i)
		}
	}
	if source, ok := legacySource("TEST-001"); !ok || source != "TEST" {
		t.Fatal("dedicated test fixture rejected")
	}
	for _, id := range []string{"SIM-000", "SIM-021", "SIM-999", "SIM-01a", "SIM-01", "SIM-P001-01", "EDGE-P001-01", "TEST-002", "unknown"} {
		if _, ok := legacySource(id); ok {
			t.Fatal("unregistered identity could be auto-provisioned", id)
		}
	}
}
