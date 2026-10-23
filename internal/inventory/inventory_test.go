package inventory

import (
	"encoding/json"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"os"
	"path/filepath"
	"testing"
)

func example(t *testing.T) contract.InventoryDocument {
	t.Helper()
	d, e := Load(examplePath())
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func clone(d contract.InventoryDocument) contract.InventoryDocument {
	b, _ := json.Marshal(d)
	var out contract.InventoryDocument
	_ = json.Unmarshal(b, &out)
	return out
}
func TestInventoryExampleNoFabricatedCompletedAssets(t *testing.T) {
	d := example(t)
	if len(d.Projects) != 9 || len(d.AssetDevices) != 10 || len(Fleet(d).Devices) != 10 {
		t.Fatal("inventory counts")
	}
	counts := map[string]int{}
	for _, p := range d.Projects {
		counts[p.Lifecycle]++
		if p.AdminCode != nil || p.LocationLabel != "" {
			t.Fatal("public example contains location combination")
		}
	}
	if counts["PLANNED"] != 5 || counts["PRE_DEVELOPMENT"] != 1 || counts["COMPLETED"] != 3 {
		t.Fatal(counts)
	}
	for _, a := range d.AssetDevices {
		if a.ProjectId >= "P007" {
			t.Fatal("invented completed-project asset")
		}
	}
}
func TestInventoryRejectsInvalidAndConflictingData(t *testing.T) {
	cases := map[string]func(*contract.InventoryDocument){
		"lifecycle":            func(d *contract.InventoryDocument) { d.Projects[0].Lifecycle = "OFFLINE" },
		"duplicate project":    func(d *contract.InventoryDocument) { d.Projects = append(d.Projects, d.Projects[0]) },
		"duplicate asset":      func(d *contract.InventoryDocument) { d.AssetDevices = append(d.AssetDevices, d.AssetDevices[0]) },
		"duplicate runtime":    func(d *contract.InventoryDocument) { d.RuntimeDevices = append(d.RuntimeDevices, d.RuntimeDevices[0]) },
		"wrong asset project":  func(d *contract.InventoryDocument) { d.RuntimeDevices[0].ProjectId = "P004" },
		"SIM identity as REAL": func(d *contract.InventoryDocument) { d.RuntimeDevices[0].SourceMode = "REAL" },
		"unknown project":      func(d *contract.InventoryDocument) { d.AssetDevices[0].ProjectId = "missing" },
		"negative tonnage":     func(d *contract.InventoryDocument) { v := -1.0; d.AssetDevices[0].RatedCapacityT = &v },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := clone(example(t))
			mutate(&d)
			if Validate(d) == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}
func TestStrictYAMLRejectsUnknownDuplicateAndMultipleDocuments(t *testing.T) {
	for _, b := range []string{"tenantId: demo\nunknown: true", "tenantId: demo\ntenantId: other", "tenantId: demo\n---\ntenantId: other"} {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		if e := os.WriteFile(path, []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Load(path); e == nil {
			t.Fatal("unsafe YAML accepted")
		}
	}
}

func examplePath() string {
	if path := os.Getenv("A0_INVENTORY_EXAMPLE"); path != "" {
		return path
	}
	return "../../deploy/inventory/inventory.example.yaml"
}
