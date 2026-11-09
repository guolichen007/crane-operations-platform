package inventory

// H2-1A-R1 R1-2: characterization of the project runtime counters returned by
// Projects() — runtimeDeviceCount, realConnectedCount, simulatedCount.
//
// These counters are NOT changed in H2-1A. Their definitions are frozen only
// when the operations summary is implemented (H2-1C); until then they must not
// be used as the authoritative homepage "on-site connected" figure. This test
// pins the CURRENT behaviour so any later change is a deliberate, reviewed one,
// and documents the gap against the Inventory asset scope:
//   - they count runtime identities (devices rows), not assets;
//   - they include identities of RETIRED assets;
//   - they include identities with no asset binding (asset_device_id IS NULL);
//   - realConnectedCount ignores observation freshness (any MQTT_CONNECTED shadow).
// When H2-1C freezes the definitions, this test must be updated together with them.

import (
	"context"
	"testing"
)

func TestInventoryProjectRuntimeCountersCurrentBehaviour(t *testing.T) {
	db := database(t)
	migrateDB(t, db, latestMigration)
	seedScope(t, db)
	for _, q := range []string{
		// Orphan runtime identity in H2A (no asset binding), connected.
		`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES ('H2-ORPHAN-REAL','H2A',NULL,'REAL')`,
		// Shadows: dual asset REAL connected; retired asset REAL connected (observed a day ago); orphan connected.
		`INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES
		  ('H2-DUAL-REAL','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"}}','2026-11-07T07:59:00Z',1),
		  ('H2-RETIRED-REAL','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-06T07:59:00Z"}}','2026-11-06T07:59:00Z',1),
		  ('H2-ORPHAN-REAL','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"}}','2026-11-07T07:59:00Z',1)`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	projects, e := Projects(context.Background(), db)
	if e != nil {
		t.Fatal(e)
	}
	var got *struct{ asset, runtime, real, sim int }
	for _, p := range projects {
		if p.Id == "H2A" {
			got = &struct{ asset, runtime, real, sim int }{p.AssetDeviceCount, p.RuntimeDeviceCount, p.RealConnectedCount, p.SimulatedCount}
		}
	}
	if got == nil {
		t.Fatal("H2A missing")
	}
	// Inventory asset scope (fixed in H2-1A): ACTIVE, OUT_OF_SERVICE, DRAFT, DUAL = 4.
	if got.asset != 4 {
		t.Errorf("assetDeviceCount = %d, want 4", got.asset)
	}
	// Runtime identities of H2A: DUAL-SIM, DUAL-REAL, RETIRED-REAL, ORPHAN-REAL = 4
	// (includes the retired asset's identity and the orphan identity).
	if got.runtime != 4 {
		t.Errorf("runtimeDeviceCount = %d, want 4 (current: all devices rows of the project)", got.runtime)
	}
	// REAL + MQTT_CONNECTED identities: DUAL-REAL, RETIRED-REAL, ORPHAN-REAL = 3
	// (no asset_status filter, no asset binding requirement, no freshness check).
	// Against the summary contract (realConnected) the same fixture would give 1.
	if got.real != 3 {
		t.Errorf("realConnectedCount = %d, want 3 (current: any REAL identity with a connected shadow)", got.real)
	}
	// SIMULATION identities: DUAL-SIM = 1 (counts identities, not assets).
	if got.sim != 1 {
		t.Errorf("simulatedCount = %d, want 1", got.sim)
	}
}
