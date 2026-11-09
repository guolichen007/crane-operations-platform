package app

// H2-1A Inventory operating scope through the real HTTP stack (OIDC session,
// project grants, real PostgreSQL). Contract: docs/04-前端与可视化/Homepage-H2-0-运营数据契约.md §2.6, §5.
//   - /api/v1/assets/devices and /api/v1/projects only show granted projects
//   - platform_admin with a P001 grant only cannot see P002
//   - a user with no grant gets 200 with empty lists (no leak)
//   - a RETIRED asset leaves the Inventory list and project count, but the
//     formal asset center (/api/v1/assets/{id}, /api/v1/assets?status=) still serves it

import (
	"context"
	"encoding/json"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

func inventoryLists(t *testing.T, f securityFixture, user string) ([]contract.InventoryProject, []contract.AssetDevice) {
	t.Helper()
	cookie, s := f.login(t, user)
	var projects []contract.InventoryProject
	var assets []contract.AssetDevice
	for path, out := range map[string]any{"/api/v1/projects": &projects, "/api/v1/assets/devices": &assets} {
		w := f.request(cookie, s.CsrfToken, path, "GET")
		if w.Code != 200 {
			t.Fatalf("%s %s: status %d %s", user, path, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return projects, assets
}

func TestH21AInventoryScopeAuthorizationAndRetiredHistory(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	// A user bound to the IdP but without any project grant.
	if err := identity.Bind(ctx, f.db, "user-nogrant", f.provider.Server.URL, "subject-nogrant", "Anonymous nogrant", true, auditlog.Event{Actor: "fixture"}); err != nil {
		t.Fatal(err)
	}

	// Baseline (example inventory): viewer "a" and platform_admin "admin" are granted P001 only.
	for _, user := range []string{"a", "admin"} {
		projects, assets := inventoryLists(t, f, user)
		if len(projects) != 1 || projects[0].Id != "P001" {
			t.Fatalf("%s projects = %+v, want only P001", user, projects)
		}
		if len(assets) == 0 {
			t.Fatalf("%s sees no P001 asset", user)
		}
		for _, a := range assets {
			if a.ProjectId != "P001" {
				t.Fatalf("%s sees foreign asset %s of %s", user, a.AssetDeviceId, a.ProjectId)
			}
		}
		if projects[0].AssetDeviceCount != len(assets) {
			t.Fatalf("%s P001 assetDeviceCount=%d, list=%d", user, projects[0].AssetDeviceCount, len(assets))
		}
	}
	// No grant: success with empty results.
	projects, assets := inventoryLists(t, f, "nogrant")
	if len(projects) != 0 || len(assets) != 0 {
		t.Fatalf("no-grant user sees projects=%d assets=%d", len(projects), len(assets))
	}

	// Retire one P001 asset: it leaves Inventory and the project count, the asset center keeps it.
	before, beforeAssets := inventoryLists(t, f, "a")
	retired := beforeAssets[0].AssetDeviceId
	if _, err := f.db.Exec(`UPDATE asset_devices SET asset_status='RETIRED' WHERE id=$1`, retired); err != nil {
		t.Fatal(err)
	}
	after, afterAssets := inventoryLists(t, f, "a")
	for _, a := range afterAssets {
		if a.AssetDeviceId == retired {
			t.Fatal("retired asset still listed by /api/v1/assets/devices")
		}
	}
	if len(afterAssets) != len(beforeAssets)-1 || after[0].AssetDeviceCount != before[0].AssetDeviceCount-1 {
		t.Fatalf("retire: list %d→%d, count %d→%d", len(beforeAssets), len(afterAssets), before[0].AssetDeviceCount, after[0].AssetDeviceCount)
	}
	if after[0].AssetDeviceCount != len(afterAssets) {
		t.Fatalf("after retire assetDeviceCount=%d, list=%d", after[0].AssetDeviceCount, len(afterAssets))
	}
	cookie, s := f.login(t, "a")
	w := f.request(cookie, s.CsrfToken, "/api/v1/assets/"+retired, "GET")
	var detail contract.AssetMasterData
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.AssetStatus != "RETIRED" {
		t.Fatalf("asset center lost retired history: %d %s", w.Code, w.Body.String())
	}
	w = f.request(cookie, s.CsrfToken, "/api/v1/assets?status=RETIRED", "GET")
	var ledger []contract.AssetMasterData
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ledger) != nil {
		t.Fatalf("asset center ledger: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, a := range ledger {
		found = found || a.Id == retired
	}
	if !found {
		t.Fatal("asset center ledger no longer lists the retired asset")
	}
}
