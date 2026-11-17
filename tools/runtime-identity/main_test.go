package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

func write(t *testing.T, v any) string {
	t.Helper()
	b, _ := json.Marshal(v)
	p := filepath.Join(t.TempDir(), "req.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFleetOutputAndRefusals(t *testing.T) {
	req := write(t, map[string]any{"tenantId": "demo", "entries": []map[string]any{{"runtimeDeviceId": "SIM-R7-01", "projectId": "P1", "assetDeviceId": "A1", "sourceMode": "SIMULATION"}}})
	var out bytes.Buffer
	if code, err := execute([]string{"fleet", "--request", req}, &out); err != nil || code != 0 {
		t.Fatalf("fleet = %d %v", code, err)
	}
	var m contract.FleetManifest
	if err := json.Unmarshal(out.Bytes(), &m); err != nil || len(m.Devices) != 1 || m.Devices[0].SourceMode != "SIMULATION" || *m.Devices[0].AssetDeviceId != "A1" {
		t.Fatalf("fleet output = %s", out.String())
	}
	real := write(t, map[string]any{"tenantId": "demo", "entries": []map[string]any{{"runtimeDeviceId": "EDGE-1", "projectId": "P1", "assetDeviceId": "A1", "sourceMode": "REAL"}}})
	for _, args := range [][]string{{"fleet", "--request", real}, {"apply", "--request", req}, {"plan", "--request", req, "--actor", "x"}, {"drop", "--request", req}, {"plan"}} {
		if _, err := execute(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://u:inline-secret@localhost/db")
	t.Setenv("DATABASE_PASSWORD_FILE", "")
	_, err := execute([]string{"plan", "--request", req}, &bytes.Buffer{})
	if err == nil || strings.Contains(safeReason(err), "inline-secret") {
		t.Fatalf("plan without password file: %v", err)
	}
}
