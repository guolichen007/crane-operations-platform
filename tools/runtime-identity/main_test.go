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

func TestMergeAndACL(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "fleet.json")
	a := "A0"
	b, _ := json.Marshal(contract.FleetManifest{TenantId: "demo", Devices: []contract.RuntimeDescriptor{{RuntimeDeviceId: "SIM-OLD", ProjectId: "P0", AssetDeviceId: &a, SourceMode: "SIMULATION"}}})
	if err := os.WriteFile(cur, b, 0o600); err != nil {
		t.Fatal(err)
	}
	req := write(t, map[string]any{"tenantId": "demo", "entries": []map[string]any{
		{"runtimeDeviceId": "SIM-NEW", "projectId": "P1", "assetDeviceId": "A1", "sourceMode": "SIMULATION"},
		{"runtimeDeviceId": "TEST-NEW", "projectId": "P1", "assetDeviceId": "A2", "sourceMode": "TEST"}}})
	var out bytes.Buffer
	if code, err := execute([]string{"merge", "--request", req, "--fleet", cur}, &out); err != nil || code != 0 {
		t.Fatalf("merge = %d %v", code, err)
	}
	var m contract.FleetManifest
	_ = json.Unmarshal(out.Bytes(), &m)
	if len(m.Devices) != 3 || m.Devices[0].RuntimeDeviceId != "SIM-OLD" {
		t.Fatalf("merged = %+v", m)
	}
	out.Reset()
	if _, err := execute([]string{"merge", "--request", req, "--fleet", cur, "--simulator"}, &out); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(out.Bytes(), &m)
	if len(m.Devices) != 2 {
		t.Fatalf("simulator fleet has %d devices, want SIMULATION only", len(m.Devices))
	}
	// Conflicting reuse of an existing id is refused.
	conflict := write(t, map[string]any{"tenantId": "demo", "entries": []map[string]any{{"runtimeDeviceId": "SIM-OLD", "projectId": "P9", "assetDeviceId": "A9", "sourceMode": "SIMULATION"}}})
	if _, err := execute([]string{"merge", "--request", conflict, "--fleet", cur}, &bytes.Buffer{}); err == nil {
		t.Fatal("conflicting merge accepted")
	}
	fl := filepath.Join(dir, "merged.json")
	merged := bytes.Buffer{}
	_, _ = execute([]string{"merge", "--request", req, "--fleet", cur}, &merged)
	_ = os.WriteFile(fl, merged.Bytes(), 0o600)
	out.Reset()
	if code, err := execute([]string{"acl", "--fleet", fl}, &out); err != nil || code != 0 {
		t.Fatalf("acl = %d %v", code, err)
	}
	acl := out.String()
	for _, want := range []string{"user SIM-NEW\n", "topic write crane/v1/demo/P1/SIM-NEW/alarm", "topic read crane/v1/demo/P1/SIM-NEW/command/watch/start", "user cloud-api\n"} {
		if !strings.Contains(acl, want) {
			t.Fatalf("acl missing %q", want)
		}
	}
	if strings.Contains(acl, "crane/v1/demo/P1/SIM-NEW/#") || strings.Contains(acl, "+") {
		t.Fatal("acl must list exact topics, no wildcards")
	}
}
