package inventory

// H2-1A Inventory operating scope on real PostgreSQL (all goose migrations applied).
// Contract: docs/04-前端与可视化/Homepage-H2-0-运营数据契约.md §2.6.
//   - Assets() lists only assets of registered Inventory projects (lifecycle IS NOT NULL)
//   - RETIRED assets are excluded; DRAFT / ACTIVE / OUT_OF_SERVICE stay registered
//   - Projects().assetDeviceCount uses the same predicate, so per-project counts sum to Assets()
//   - an asset with several runtime identities (SIM + REAL) is listed once
//   - the formal asset table keeps RETIRED rows (asset-center history is not deleted)

import (
	"context"
	"database/sql"
	"sort"
	"testing"
)

const latestMigration = 12

func seedScope(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if e := Import(ctx, db, example(t)); e != nil {
		t.Fatal(e)
	}
	// Extra fixtures next to the unchanged 9-project / 10-asset example.
	for _, q := range []string{
		`INSERT INTO projects(id,tenant_id,name,lifecycle,admin_code) VALUES ('H2A','demo','H2 范围项目','PLANNED',330481),('H2B','demo','H2 另一项目','PLANNED',440300)`,
		`INSERT INTO projects(id,tenant_id,name) VALUES ('H2LEGACY','demo','遗留项目')`, // lifecycle NULL
		`INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_status) VALUES
		  ('H2-ACTIVE','H2A','1号行车','PLANNED','ACTIVE'),
		  ('H2-OOS','H2A','停用行车','PLANNED','OUT_OF_SERVICE'),
		  ('H2-DRAFT','H2A','草稿行车','PLANNED','DRAFT'),
		  ('H2-RETIRED','H2A','退役行车','PLANNED','RETIRED'),
		  ('H2-DUAL','H2A','双身份行车','PLANNED','ACTIVE'),
		  ('H2-B-SAME','H2B','1号行车','PLANNED','ACTIVE'),
		  ('H2-LEGACY','H2LEGACY','遗留行车','PLANNED','ACTIVE')`,
		`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES
		  ('H2-DUAL-SIM','H2A','H2-DUAL','SIMULATION'),('H2-DUAL-REAL','H2A','H2-DUAL','REAL'),
		  ('H2-RETIRED-REAL','H2A','H2-RETIRED','REAL'),('H2-LEGACY-REAL','H2LEGACY','H2-LEGACY','REAL')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
}

func assetIDs(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	list, e := Assets(context.Background(), db)
	if e != nil {
		t.Fatal(e)
	}
	ids := map[string]string{}
	for _, a := range list {
		if _, dup := ids[a.AssetDeviceId]; dup {
			t.Fatalf("asset %s listed twice (runtime identities must not duplicate assets)", a.AssetDeviceId)
		}
		ids[a.AssetDeviceId] = a.ProjectId
	}
	return ids
}

func TestInventoryScopeStatusLifecycleAndDedup(t *testing.T) {
	db := database(t)
	migrateDB(t, db, latestMigration)
	seedScope(t, db)
	ids := assetIDs(t, db)
	for id, want := range map[string]bool{
		"H2-ACTIVE":  true,  // registered project + ACTIVE
		"H2-OOS":     true,  // OUT_OF_SERVICE stays registered
		"H2-DRAFT":   true,  // DRAFT stays registered
		"H2-RETIRED": false, // RETIRED excluded from Inventory
		"H2-LEGACY":  false, // lifecycle NULL project excluded
		"H2-DUAL":    true,  // SIM + REAL identities → one asset
		"H2-B-SAME":  true,  // same display name in another project, distinct id
	} {
		if _, got := ids[id]; got != want {
			t.Errorf("Assets(): %s present=%v, want %v", id, got, want)
		}
	}
	if ids["H2-ACTIVE"] != "H2A" || ids["H2-B-SAME"] != "H2B" {
		t.Errorf("same-name assets must keep their own project: %v / %v", ids["H2-ACTIVE"], ids["H2-B-SAME"])
	}
	// Representative runtime identity of the dual asset stays REAL (unchanged rule).
	list, _ := Assets(context.Background(), db)
	for _, a := range list {
		if a.AssetDeviceId == "H2-DUAL" && (a.RuntimeSource == nil || *a.RuntimeSource != "REAL") {
			t.Errorf("dual asset representative runtime = %v, want REAL", a.RuntimeSource)
		}
	}
	// The example inventory (10 assets in 9 projects) is unchanged.
	example := 0
	for id := range ids {
		if len(id) > 6 && id[:6] == "ASSET-" {
			example++
		}
	}
	if example != 10 {
		t.Errorf("example Inventory assets = %d, want 10", example)
	}
	// Runtime identities and their binding are untouched by the scope rule.
	if count(t, db, `SELECT count(*) FROM devices WHERE asset_device_id='H2-DUAL'`) != 2 {
		t.Error("runtime identities of the dual asset changed")
	}
}

func TestInventoryProjectCountsMatchAssetList(t *testing.T) {
	db := database(t)
	migrateDB(t, db, latestMigration)
	seedScope(t, db)
	ctx := context.Background()
	// Stable data: the fixture is fully written before both reads and nothing writes concurrently.
	projects, e := Projects(ctx, db)
	if e != nil {
		t.Fatal(e)
	}
	ids := assetIDs(t, db)
	perProject := map[string]int{}
	for _, project := range ids {
		perProject[project]++
	}
	sum := 0
	listed := map[string]bool{}
	for _, p := range projects {
		listed[p.Id] = true
		sum += p.AssetDeviceCount
		if p.AssetDeviceCount != perProject[p.Id] {
			t.Errorf("project %s assetDeviceCount=%d, asset list has %d", p.Id, p.AssetDeviceCount, perProject[p.Id])
		}
	}
	if sum != len(ids) {
		t.Errorf("Σ assetDeviceCount=%d, len(Assets)=%d", sum, len(ids))
	}
	for id, project := range ids {
		if !listed[project] {
			t.Errorf("asset %s belongs to project %s that Projects() does not list", id, project)
		}
	}
	if listed["H2LEGACY"] {
		t.Error("legacy lifecycle=NULL project listed")
	}
}

func TestInventoryScopeKeepsRetiredAssetHistory(t *testing.T) {
	db := database(t)
	migrateDB(t, db, latestMigration)
	seedScope(t, db)
	// The asset-center table still holds the retired asset and its runtime identity.
	if count(t, db, `SELECT count(*) FROM asset_devices WHERE id='H2-RETIRED' AND asset_status='RETIRED'`) != 1 {
		t.Fatal("retired asset history lost")
	}
	if count(t, db, `SELECT count(*) FROM devices WHERE asset_device_id='H2-RETIRED'`) != 1 {
		t.Fatal("retired asset runtime identity lost")
	}
	// The default added by migration 00005 is ACTIVE and NOT NULL: a row inserted
	// without asset_status is in scope (no NULL status can slip through <> 'RETIRED').
	if count(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_name='asset_devices' AND column_name='asset_status' AND is_nullable='NO' AND column_default LIKE '''ACTIVE''%'`) != 1 {
		t.Fatal("asset_status is not NOT NULL DEFAULT 'ACTIVE'")
	}
	ids := assetIDs(t, db)
	keys := make([]string, 0, len(ids))
	for k := range ids {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "H2-RETIRED" {
			t.Fatal("retired asset listed by Inventory")
		}
	}
}
