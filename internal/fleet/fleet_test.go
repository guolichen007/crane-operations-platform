package fleet

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyCountAndManifestPriority(t *testing.T) {
	old, e := Load("", 20, false, true)
	if e != nil || len(old.Devices) != 20 || old.Devices[19].RuntimeDeviceId != "SIM-020" {
		t.Fatal("legacy count mode lost", e)
	}
	path := filepath.Join(t.TempDir(), "fleet.yaml")
	b := `tenantId: demo
devices:
  - runtimeDeviceId: SIM-P001-01
    projectId: P001
    assetDeviceId: ASSET-P001-01
    sourceMode: SIMULATION
  - runtimeDeviceId: SIM-P004-02
    projectId: P004
    assetDeviceId: ASSET-P004-02
    sourceMode: SIMULATION
`
	if e = os.WriteFile(path, []byte(b), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := Load(path, 20, false, true)
	if e != nil || len(m.Devices) != 2 {
		t.Fatal("manifest did not override count", e)
	}
	for _, d := range Devices(m) {
		if d.TenantID != "demo" || d.ProjectId == "Project-A" {
			t.Fatal("descriptor lost")
		}
	}
	acl, e := ACL(m)
	if e != nil {
		t.Fatal(e)
	}
	first := strings.Split(strings.Split(acl, "user SIM-P001-01\n")[1], "user SIM-P004-02")[0]
	if strings.Contains(first, "P004") || strings.Contains(acl, "%u") || strings.Contains(acl, "crane/v1/+/+") {
		t.Fatal("cross-project wildcard ACL")
	}
	again, _ := ACL(m)
	if acl != again {
		t.Fatal("non-deterministic ACL")
	}
	message := contract.Envelope{TenantId: "demo", ProjectId: "P004", DeviceId: "SIM-P001-01"}
	if Allows(m, message) {
		t.Fatal("cross-project envelope accepted")
	}
	message.ProjectId = "P001"
	if !Allows(m, message) {
		t.Fatal("own scope rejected")
	}
	m.Devices[0].SourceMode = "REAL"
	if Validate(m, true) == nil {
		t.Fatal("Mock accepted real source")
	}
	if _, e = Load(path+"missing", 20, false, true); e == nil {
		t.Fatal("missing manifest silently fell back to count")
	}
}
