package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigurationPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if e := os.WriteFile(path, []byte(`{"count":2,"http":":9000"}`), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("APP_CONFIG", path)
	t.Setenv("SIM_COUNT", "3")
	c, e := Load([]string{"--count", "4"})
	if e != nil || c.Count != 4 || c.HTTP != ":9000" {
		t.Fatal("precedence", e)
	}
	if _, e = Load([]string{"--count", "21"}); e == nil {
		t.Fatal("unsupported fleet accepted")
	}
	t.Setenv("SIM_SCENARIO", "real-control")
	if _, e = Load(nil); e == nil {
		t.Fatal("unknown scenario accepted")
	}
}

func TestFleetFileTakesPriorityOverLegacyCountBounds(t *testing.T) {
	c, e := Load([]string{"--fleet-file", "explicit-fleet.yaml", "--count", "0"})
	if e != nil || c.FleetFile != "explicit-fleet.yaml" {
		t.Fatal("manifest did not take priority", e)
	}
	if _, e = Load([]string{"--count", "0"}); e == nil {
		t.Fatal("legacy count bounds removed")
	}
}
