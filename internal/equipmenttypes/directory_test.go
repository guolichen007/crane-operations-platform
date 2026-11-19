package equipmenttypes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/guolichen007/crane-operations-platform/deploy/postgres/migrations"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/asset"
	"github.com/guolichen007/crane-operations-platform/internal/equipment"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func allScope(t *testing.T) []string {
	t.Helper()
	s, err := Scope(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// F-02 root cause + bootstrap: a freshly migrated schema-12 database has an empty
// type directory and refuses every install; plan → apply initializes exactly the
// registry, after which an Equipment installs.
func TestFreshSchema12BootstrapMakesEquipmentInstallable(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	var schema int64
	if err := db.QueryRow(`SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&schema); err != nil || schema != migrations.Latest() {
		t.Fatalf("schema version = %d (err=%v), want 12", schema, err)
	}
	if n := count(t, db, `SELECT count(*) FROM equipment_types`); n != 0 {
		t.Fatalf("migrations seeded %d equipment types; F-02 premise is an empty directory", n)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,tenant_id,name) VALUES('P-R6','demo','P-R6')`); err != nil {
		t.Fatal(err)
	}
	a, err := asset.CreateAsset(ctx, db, contract.AssetMasterData{ProjectId: "P-R6", AssetType: "CRANE", AssetCode: ptr("CRN-R6")}, "actor", "req-a", "")
	if err != nil {
		t.Fatal(err)
	}
	motor := contract.EquipmentInstallation{AssetId: a.Id, EquipmentCode: "EQ-1", TypeCode: "MOTOR", Criticality: "MEDIUM"}
	if _, err := asset.CreateEquipment(ctx, db, motor, json.RawMessage(`{}`), "actor", "req-e0", ""); !errors.Is(err, asset.ErrInvalidInput) {
		t.Fatalf("install on an empty directory: err = %v, want ErrInvalidInput", err)
	}

	scope := allScope(t)
	p, err := PlanTypes(ctx, db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ToCreate) != len(equipment.Types()) || len(p.Existing)+len(p.Disabled)+len(p.Conflicts)+len(p.Unregistered) != 0 {
		t.Fatalf("plan on empty directory = %+v", p)
	}
	if n := count(t, db, `SELECT count(*) FROM equipment_types`); n != 0 {
		t.Fatal("plan wrote rows")
	}
	if n := count(t, db, `SELECT count(*) FROM audit_logs WHERE action='equipment_types_initialized'`); n != 0 {
		t.Fatal("plan wrote an audit row")
	}
	rec, err := Apply(ctx, db, scope, p.Digest, "ops-admin", "req-apply-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Result != "success" || len(rec.Created) != len(equipment.Types()) || rec.Actor != "host:ops-admin" || rec.Target.SchemaVersion != migrations.Latest() || rec.Target.Database == "" {
		t.Fatalf("apply record = %+v", rec)
	}
	if n := count(t, db, `SELECT count(*) FROM equipment_types WHERE status='ACTIVE'`); n != len(equipment.Types()) {
		t.Fatalf("active types = %d, want %d", n, len(equipment.Types()))
	}
	if n := count(t, db, `SELECT count(*) FROM audit_logs WHERE action='equipment_types_initialized' AND result='success' AND user_id='host:ops-admin' AND request_id='req-apply-1'`); n != 1 {
		t.Fatalf("apply audit rows = %d, want 1", n)
	}
	r, err := Verify(ctx, db, scope)
	if err != nil || !r.Consistent || len(r.Installable) != len(equipment.Types()) {
		t.Fatalf("verify after apply = %+v (err=%v)", r, err)
	}
	if _, err := asset.CreateEquipment(ctx, db, motor, json.RawMessage(`{}`), "actor", "req-e1", ""); err != nil {
		t.Fatalf("install after bootstrap: %v", err)
	}
	if n := count(t, db, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`); int64(n) != migrations.Latest() {
		t.Fatalf("schema changed to %d", n)
	}
}

// Second apply: the plan is all EXISTING, apply is a recorded noop and creates
// nothing; a stale digest (from before the first apply) is refused.
func TestApplyIsRepeatableAndDigestBound(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	scope := allScope(t)
	first, _ := PlanTypes(ctx, db, scope)
	if _, err := Apply(ctx, db, scope, first.Digest, "ops", "r1"); err != nil {
		t.Fatal(err)
	}
	// Re-presenting the first approval after the state changed is refused.
	if _, err := Apply(ctx, db, scope, first.Digest, "ops", "r2"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("stale digest: err = %v, want ErrDigestMismatch", err)
	}
	second, _ := PlanTypes(ctx, db, scope)
	if len(second.ToCreate) != 0 || len(second.Existing) != len(scope) {
		t.Fatalf("second plan = %+v", second)
	}
	rec, err := Apply(ctx, db, scope, second.Digest, "ops", "r3")
	if err != nil || rec.Result != "noop" || len(rec.Created) != 0 {
		t.Fatalf("second apply = %+v (err=%v)", rec, err)
	}
	if n := count(t, db, `SELECT count(*) FROM equipment_types`); n != len(scope) {
		t.Fatalf("rows after second apply = %d, want %d", n, len(scope))
	}
	if n := count(t, db, `SELECT count(*) FROM audit_logs WHERE action='equipment_types_initialized'`); n != 2 {
		t.Fatalf("audit rows = %d, want 2 (success + noop; the refused apply writes none)", n)
	}
	// Missing actor / empty scope / wrong digest never write.
	if _, err := Apply(ctx, db, scope, second.Digest, " ", "r4"); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("missing actor: %v", err)
	}
	if _, err := Apply(ctx, db, nil, second.Digest, "ops", "r5"); !errors.Is(err, ErrEmptyScope) {
		t.Fatalf("empty scope: %v", err)
	}
	if _, err := Apply(ctx, db, scope, "deadbeef", "ops", "r6"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("forged digest: %v", err)
	}
}

// DISABLED rows are never re-enabled; conflicting rows refuse the apply;
// unregistered rows are reported as drift and never touched; codes outside the
// registry are refused before any database access.
func TestDisabledConflictAndUnregisteredProtection(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := Scope(false, []string{"MOTOR", "EVIL'; DROP TABLE x;--"}); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("unregistered code in scope: %v", err)
	}
	if _, err := Scope(false, nil); !errors.Is(err, ErrEmptyScope) {
		t.Fatalf("empty scope: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain,spec_schema_version,status) VALUES
		('MOTOR','电机','MECHANICAL','1','DISABLED'),
		('PLC','Wrong name','CONTROL','1','ACTIVE'),
		('LEGACY_X','legacy','OTHER','1','ACTIVE')`); err != nil {
		t.Fatal(err)
	}
	scope, _ := Scope(false, []string{"MOTOR", "VFD"})
	p, err := PlanTypes(ctx, db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Disabled) != 1 || p.Disabled[0] != "MOTOR" || len(p.ToCreate) != 1 || p.ToCreate[0] != "VFD" || len(p.Conflicts) != 0 || len(p.Unregistered) != 1 {
		t.Fatalf("plan = %+v", p)
	}
	rec, err := Apply(ctx, db, scope, p.Digest, "ops", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Created) != 1 || rec.Created[0] != "VFD" || len(rec.Disabled) != 1 {
		t.Fatalf("record = %+v", rec)
	}
	var status string
	var version int64
	if err := db.QueryRow(`SELECT status, version FROM equipment_types WHERE type_code='MOTOR'`).Scan(&status, &version); err != nil || status != "DISABLED" || version != 1 {
		t.Fatalf("MOTOR = %s v%d (err=%v), want DISABLED v1 untouched", status, version, err)
	}
	// A conflicting row in scope refuses everything (no partial create).
	conflictScope, _ := Scope(false, []string{"PLC", "IMU"})
	cp, _ := PlanTypes(ctx, db, conflictScope)
	if len(cp.Conflicts) != 1 || cp.Conflicts[0] != "PLC" {
		t.Fatalf("conflict plan = %+v", cp)
	}
	if _, err := Apply(ctx, db, conflictScope, cp.Digest, "ops", "r2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("apply with conflict: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM equipment_types WHERE type_code='IMU'`); n != 0 {
		t.Fatal("conflicting apply created rows")
	}
	if n := count(t, db, `SELECT count(*) FROM equipment_types WHERE type_code='PLC' AND display_name='Wrong name'`); n != 1 {
		t.Fatal("conflicting row was rewritten")
	}
	r, err := Verify(ctx, db, allScope(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Consistent || len(r.Plan.Unregistered) != 1 || r.Plan.Unregistered[0] != "LEGACY_X" {
		t.Fatalf("verify = %+v", r)
	}
	for _, c := range r.Installable {
		if c == "MOTOR" || c == "PLC" {
			t.Fatalf("%s listed installable", c)
		}
	}
	if n := count(t, db, `SELECT count(*) FROM equipment_types WHERE type_code='LEGACY_X'`); n != 1 {
		t.Fatal("unregistered row was touched")
	}
}

// Apply leaves existing installations and their history exactly as they were.
func TestApplyDoesNotTouchInstallationsOrHistory(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO projects(id,tenant_id,name) VALUES('P-R6','demo','P-R6')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain,spec_schema_version,status) VALUES('MOTOR','电机','MECHANICAL','1','ACTIVE')`); err != nil {
		t.Fatal(err)
	}
	a, _ := asset.CreateAsset(ctx, db, contract.AssetMasterData{ProjectId: "P-R6", AssetType: "CRANE", AssetCode: ptr("CRN-H")}, "actor", "req-a", "")
	eq, err := asset.CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: a.Id, EquipmentCode: "EQ-H", TypeCode: "MOTOR", Criticality: "LOW"}, json.RawMessage(`{}`), "actor", "req-e", "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var s string
		if err := db.QueryRow(`SELECT (SELECT json_agg(row_to_json(e) ORDER BY id)::text FROM asset_equipment_installations e) || (SELECT json_agg(json_build_array(entity_type,entity_id,revision,snapshot_sha256) ORDER BY id)::text FROM entity_revisions)`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	scope := allScope(t)
	p, _ := PlanTypes(ctx, db, scope)
	if _, err := Apply(ctx, db, scope, p.Digest, "ops", "r1"); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("apply changed installations/history for %s", eq.Id)
	}
}

func ptr(s string) *string { return &s }

// The operator runbook and the deployment acceptance initialize exactly 51 types (43 + 8 R9); a registry
// change must update the runbook (docs/08-部署运维与成本/R6-Equipment设备类型目录初始化操作手册.md).
func TestRegistryCountMatchesRunbook(t *testing.T) {
	if n := len(equipment.Types()); n != 51 {
		t.Fatalf("code registry lists %d types; the runbook and acceptance expect 51", n)
	}
	s, err := Scope(true, nil)
	if err != nil || len(s) != 51 {
		t.Fatalf("--all scope = %d (err=%v), want 51", len(s), err)
	}
}

// A plan computed on one database cannot be applied to another: the target identity is part
// of the digest.
func TestPlanDigestBoundToDatabase(t *testing.T) {
	ctx := context.Background()
	db1 := testsupport.Database(t)
	db2 := testsupport.Database(t)
	scope, _ := Scope(false, []string{"MOTOR"})
	p1, err := PlanTypes(ctx, db1, scope)
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := PlanTypes(ctx, db2, scope)
	if p1.Digest == p2.Digest || p1.Target.Database == p2.Target.Database {
		t.Fatalf("identical empty databases produced the same digest/target: %+v / %+v", p1.Target, p2.Target)
	}
	if _, err := Apply(ctx, db2, scope, p1.Digest, "ops", "x"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("cross-database apply: %v", err)
	}
	if n := count(t, db2, `SELECT count(*) FROM equipment_types`); n != 0 {
		t.Fatal("cross-database apply wrote rows")
	}
}
