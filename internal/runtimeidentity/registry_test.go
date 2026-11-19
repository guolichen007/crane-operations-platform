package runtimeidentity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/guolichen007/crane-operations-platform/deploy/postgres/migrations"
	"sync"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/asset"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func n(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var v int
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func seed(t *testing.T, db *sql.DB) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	for _, p := range []string{"R7-P1", "R7-P2"} {
		if _, err := db.Exec(`INSERT INTO projects(id,tenant_id,name,lifecycle) VALUES($1,'demo',$1,'PLANNED')`, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain) VALUES('PLC','PLC 控制器','CONTROL')`); err != nil {
		t.Fatal(err)
	}
	code := func(s string) *string { return &s }
	a1, err := asset.CreateAsset(ctx, db, contract.AssetMasterData{ProjectId: "R7-P1", AssetType: "CRANE", AssetCode: code("R7-A1")}, "a", "r", "")
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := asset.CreateAsset(ctx, db, contract.AssetMasterData{ProjectId: "R7-P2", AssetType: "CRANE", AssetCode: code("R7-A2")}, "a", "r", "")
	eq, err := asset.CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: a1.Id, EquipmentCode: "PLC-1", TypeCode: "PLC", Criticality: "HIGH"}, json.RawMessage(`{}`), "a", "r", "")
	if err != nil {
		t.Fatal(err)
	}
	return a1.Id, a2.Id, eq.Id
}

func TestRegisterPlanApplyVerifyAndBinding(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	a1, a2, eq := seed(t, db)
	req := Request{TenantId: "demo", Entries: []Entry{
		{RuntimeDeviceId: "SIM-R7-001", ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION", EquipmentInstallationId: &eq},
		{RuntimeDeviceId: "SIM-R7-002", ProjectId: "R7-P2", AssetDeviceId: a2, SourceMode: "SIMULATION"},
	}}
	p, err := PlanRegistration(ctx, db, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Items[0].Status != StatusNew || p.Items[0].Binding != "NEW" || p.Items[1].Status != StatusNew || p.Target.SchemaVersion != migrations.Latest() {
		t.Fatalf("plan = %+v", p)
	}
	if n(t, db, `SELECT count(*) FROM devices WHERE id LIKE 'SIM-R7-%'`) != 0 {
		t.Fatal("plan wrote")
	}
	rec, err := Apply(ctx, db, req, p.Digest, "ops", "req-1")
	if err != nil || rec.Result != "success" || len(rec.Created) != 2 || len(rec.Bound) != 1 {
		t.Fatalf("apply = %+v %v", rec, err)
	}
	if _, err := Apply(ctx, db, req, p.Digest, "ops", "req-2"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("stale digest: %v", err)
	}
	v, _ := Verify(ctx, db, req)
	if !v.Consistent {
		t.Fatalf("verify = %+v", v)
	}
	p2, _ := PlanRegistration(ctx, db, req)
	rec2, err := Apply(ctx, db, req, p2.Digest, "ops", "req-3")
	if err != nil || rec2.Result != "noop" || len(rec2.Created) != 0 {
		t.Fatalf("second apply = %+v %v", rec2, err)
	}
	if n(t, db, `SELECT count(*) FROM devices WHERE id LIKE 'SIM-R7-%' AND source_mode='SIMULATION'`) != 2 ||
		n(t, db, `SELECT count(*) FROM equipment_runtime_bindings WHERE source_ref='SIM-R7-001' AND valid_to IS NULL`) != 1 ||
		n(t, db, `SELECT count(*) FROM audit_logs WHERE action IN ('runtime_identity_registered','equipment_runtime_bound') AND result='success' AND user_id='host:ops'`) != 3 {
		t.Fatal("rows/audit mismatch")
	}
	// Item 12: an asset with an active runtime binding cannot be retired; its Equipment cannot be removed.
	var version int64
	_ = db.QueryRow(`SELECT version FROM asset_devices WHERE id=$1`, a1).Scan(&version)
	if err := asset.RetireAsset(ctx, db, a1, version, "a", "ret"); !errors.Is(err, asset.ErrActiveBinding) {
		t.Fatalf("retire with active binding: %v", err)
	}
	var eqVersion int64
	_ = db.QueryRow(`SELECT version FROM asset_equipment_installations WHERE id=$1`, eq).Scan(&eqVersion)
	if err := asset.RemoveEquipment(ctx, db, eq, eqVersion, "a", "rm"); !errors.Is(err, asset.ErrActiveBinding) {
		t.Fatalf("remove bound equipment: %v", err)
	}
}

func TestRegisterRefusals(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	a1, a2, eq := seed(t, db)
	for name, req := range map[string]Request{
		"REAL refused":         {TenantId: "demo", Entries: []Entry{{RuntimeDeviceId: "EDGE-1", ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "REAL"}}},
		"prefix mismatch":      {TenantId: "demo", Entries: []Entry{{RuntimeDeviceId: "EDGE-1", ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION"}}},
		"reserved id":          {TenantId: "demo", Entries: []Entry{{RuntimeDeviceId: "cloud-api", ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION"}}},
		"duplicate in request": {TenantId: "demo", Entries: []Entry{{RuntimeDeviceId: "SIM-X", ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION"}, {RuntimeDeviceId: "SIM-X", ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION"}}},
		"empty":                {TenantId: "demo"},
	} {
		if _, err := PlanRegistration(ctx, db, req); !errors.Is(err, ErrRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	invalid := func(name string, e Entry, wantStatus string) {
		t.Helper()
		req := Request{TenantId: "demo", Entries: []Entry{e}}
		p, err := PlanRegistration(ctx, db, req)
		if err != nil || p.Items[0].Status != wantStatus {
			t.Fatalf("%s: plan = %+v %v", name, p, err)
		}
		if _, err := Apply(ctx, db, req, p.Digest, "ops", "x"); !errors.Is(err, ErrNotApplicable) {
			t.Fatalf("%s: apply = %v", name, err)
		}
	}
	invalid("asset of another project", Entry{RuntimeDeviceId: "SIM-R7-X1", ProjectId: "R7-P1", AssetDeviceId: a2, SourceMode: "SIMULATION"}, StatusInvalid)
	invalid("unknown project (never created)", Entry{RuntimeDeviceId: "SIM-R7-X2", ProjectId: "R7-NOPE", AssetDeviceId: a1, SourceMode: "SIMULATION"}, StatusInvalid)
	invalid("equipment of another asset", Entry{RuntimeDeviceId: "SIM-R7-X3", ProjectId: "R7-P2", AssetDeviceId: a2, SourceMode: "SIMULATION", EquipmentInstallationId: &eq}, StatusInvalid)
	// Existing identity with a different project/source is immutable.
	if _, err := db.Exec(`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES('SIM-R7-OLD','R7-P2',$1,'SIMULATION')`, a2); err != nil {
		t.Fatal(err)
	}
	invalid("identity reassignment", Entry{RuntimeDeviceId: "SIM-R7-OLD", ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION"}, StatusConflict)
	invalid("second SIMULATION runtime for one asset", Entry{RuntimeDeviceId: "SIM-R7-NEW", ProjectId: "R7-P2", AssetDeviceId: a2, SourceMode: "SIMULATION"}, StatusConflict)
	// Retired asset is terminal.
	var version int64
	_ = db.QueryRow(`SELECT version FROM asset_devices WHERE id=$1`, a1).Scan(&version)
	if err := asset.RetireAsset(ctx, db, a1, version, "a", "ret"); err != nil {
		t.Fatal(err)
	}
	invalid("retired asset", Entry{RuntimeDeviceId: "SIM-R7-RET", ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION"}, StatusInvalid)
	if n(t, db, `SELECT count(*) FROM devices WHERE id LIKE 'SIM-R7-%'`) != 1 || n(t, db, `SELECT count(*) FROM projects WHERE id='R7-NOPE'`) != 0 {
		t.Fatal("refused requests wrote rows")
	}
	// The SIM identity is never changed into REAL.
	var src string
	_ = db.QueryRow(`SELECT source_mode FROM devices WHERE id='SIM-R7-OLD'`).Scan(&src)
	if src != "SIMULATION" {
		t.Fatal("source mode changed")
	}
}

// Concurrent terminal transitions: a binding apply races RemoveEquipment and RetireAsset on the
// same asset. Whatever the interleaving, the database never ends with an active binding on a
// removed installation or a retired asset.
func TestConcurrentBindingVersusRemoveAndRetire(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	for round := 0; round < 12; round++ {
		a1, _, eq := seed2(t, db, round)
		// The runtime exists first (no binding), so the apply only adds a binding.
		pre := Request{TenantId: "demo", Entries: []Entry{{RuntimeDeviceId: fmt.Sprintf("SIM-RC-%02d", round), ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION"}}}
		pp, _ := PlanRegistration(ctx, db, pre)
		if _, err := Apply(ctx, db, pre, pp.Digest, "ops", "pre"); err != nil {
			t.Fatal(err)
		}
		req := Request{TenantId: "demo", Entries: []Entry{{RuntimeDeviceId: fmt.Sprintf("SIM-RC-%02d", round), ProjectId: "R7-P1", AssetDeviceId: a1, SourceMode: "SIMULATION", EquipmentInstallationId: &eq}}}
		p, err := PlanRegistration(ctx, db, req)
		if err != nil || p.Items[0].Binding != "NEW" {
			t.Fatalf("plan = %+v %v", p, err)
		}
		var version, eqVersion int64
		_ = db.QueryRow(`SELECT version FROM asset_devices WHERE id=$1`, a1).Scan(&version)
		_ = db.QueryRow(`SELECT version FROM asset_equipment_installations WHERE id=$1`, eq).Scan(&eqVersion)
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, _ = Apply(ctx, db, req, p.Digest, "ops", "race") }()
		go func() {
			defer wg.Done()
			<-start
			if round%2 == 0 {
				_ = asset.RemoveEquipment(ctx, db, eq, eqVersion, "u", "rm")
			} else {
				_ = asset.RetireAsset(ctx, db, a1, version, "u", "ret")
			}
		}()
		close(start)
		wg.Wait()
		var bad int
		_ = db.QueryRow(`SELECT count(*) FROM equipment_runtime_bindings b JOIN asset_equipment_installations e ON e.id=b.equipment_installation_id JOIN asset_devices a ON a.id=b.asset_id
		  WHERE b.valid_to IS NULL AND (e.valid_to IS NOT NULL OR a.asset_status='RETIRED')`).Scan(&bad)
		if bad != 0 {
			t.Fatalf("round %d: %d active bindings on a removed installation / retired asset", round, bad)
		}
	}
}

func seed2(t *testing.T, db *sql.DB, round int) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO projects(id,tenant_id,name,lifecycle) VALUES('R7-P1','demo','R7-P1','PLANNED') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain) VALUES('PLC','PLC 控制器','CONTROL') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	code := fmt.Sprintf("RC-%02d", round)
	a, err := asset.CreateAsset(ctx, db, contract.AssetMasterData{ProjectId: "R7-P1", AssetType: "CRANE", AssetCode: &code}, "a", "r", "")
	if err != nil {
		t.Fatal(err)
	}
	eq, err := asset.CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: a.Id, EquipmentCode: "PLC", TypeCode: "PLC", Criticality: "HIGH"}, json.RawMessage(`{}`), "a", "r", "")
	if err != nil {
		t.Fatal(err)
	}
	return a.Id, "", eq.Id
}
