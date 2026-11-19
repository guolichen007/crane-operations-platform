package onboarding

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func ready(t *testing.T) *sql.DB {
	t.Helper()
	db := testsupport.Database(t)
	ctx := context.Background()
	scope, err := equipmenttypes.Scope(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := equipmenttypes.PlanTypes(ctx, db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = equipmenttypes.Apply(ctx, db, scope, p.Digest, "ops", "types"); err != nil {
		t.Fatal(err)
	}
	return db
}

func n(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var v int
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatal(q, err)
	}
	return v
}

func TestStagingDocumentIsCompleteAndConsistent(t *testing.T) {
	b, sum := Embedded()
	s, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if sum != "438de13b3e2407631f4f105ec678341a771a993bf7041a2b5ca457fc6a35b6ed" || s.Source.SHA256 != "c35f5f39fb06eaeb3248f7b69addbe010bd42166949eb2e95f9b6859e8c6cf87" {
		t.Fatalf("staging %s / source %s", sum, s.Source.SHA256)
	}
	if len(s.Records) != 55 || s.Counts.EquipmentCandidateRows != 51 || s.Counts.OperationRows != 4 {
		t.Fatalf("rows %d", len(s.Records))
	}
	d, err := build(s, sum)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.equipment) != 51 || len(d.points) != 55 {
		t.Fatalf("equipment %d points %d", len(d.equipment), len(d.points))
	}
	// Explicit address modelling: DBX bit vs INT/FLOAT published value; the address without a bit
	// index is UNPARSED (never completed); 3 duplicate groups and every R9 review item are present.
	kinds := map[string]int{}
	for _, p := range d.points {
		kinds[p.Addr.Kind]++
		if p.Code == "LHK-P021" && (p.Addr.Kind != "UNPARSED" || *p.Addr.Raw != "DB505.DBX66") {
			t.Fatalf("missing bit index must stay UNPARSED: %+v", p.Addr)
		}
	}
	if kinds["BIT"] != 47 || kinds["UNPARSED"] != 1 || kinds["NONE"] != 7 {
		t.Fatalf("address kinds %v", kinds)
	}
	codes := map[string]int{}
	for _, is := range d.issues {
		codes[is.Code]++
	}
	for code, want := range map[string]int{"DUPLICATE_PLC_ADDRESS_GROUP": 3, "MISSING_BIT_INDEX": 1, "THERMAL_RELAY_HOURS_VS_COUNT": 1, "UNIT_CONFLICT_HOURS_VS_TONNES": 1, "CHECK_RAW_BIT_VS_PUBLISHED_METRIC_TYPE": 48} {
		if codes[code] != want {
			t.Fatalf("issue %s = %d, want %d (%v)", code, codes[code], want, codes)
		}
	}
	// One raw signal → several meters (brake contactor: cycles + mechanism running hours).
	perPoint := map[string][]string{}
	for _, m := range d.meters {
		perPoint[m.PointCode] = append(perPoint[m.PointCode], m.Metric)
	}
	for _, p := range []string{"LHK-P002", "LHK-P015", "LHK-P026"} {
		if strings.Join(perPoint[p], ",") != "cycles,mechanism_running_hours" {
			t.Fatalf("%s meters %v", p, perPoint[p])
		}
	}
	types := map[string]bool{}
	for _, e := range d.equipment {
		types[e.TypeCode] = true
	}
	for _, want := range []string{"WIRE_ROPE", "RADAR", "RFID_READER", "LIDAR", "THERMAL_OVERLOAD_RELAY", "SOLENOID_VALVE", "BARRIER_GATE", "MOTOR_PROTECTOR"} {
		if !types[want] {
			t.Fatalf("type %s not used", want)
		}
	}
}

// A4/A5 end to end on PostgreSQL: plan (read-only) → apply (digest-bound, one transaction) →
// verify; repeat is a noop (no duplicates); a changed document is refused; original rows lossless.
func TestImportPlanApplyVerifyIdempotent(t *testing.T) {
	db := ready(t)
	ctx := context.Background()
	b, _ := Embedded()
	p, _, err := PlanImport(ctx, db, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Conflicts) != 0 || p.New["equipment"] != 51 || p.New["source_row"] != 55 || p.New["project"] != 1 || p.New["asset"] != 1 {
		t.Fatalf("plan %+v", p.New)
	}
	if n(t, db, `SELECT count(*) FROM asset_devices`) != 0 {
		t.Fatal("plan wrote")
	}
	if _, err = Apply(ctx, db, b, "0000", "ops", "r1"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("wrong digest: %v", err)
	}
	if _, err = Apply(ctx, db, b, p.Digest, "", "r1"); !errors.Is(err, ErrActorRequired) {
		t.Fatal("actor required")
	}
	rec, err := Apply(ctx, db, b, p.Digest, "ops-admin", "r1")
	if err != nil || rec.Result != "success" || rec.Created["equipment"] != 51 {
		t.Fatalf("apply %+v %v", rec, err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM projects WHERE id='LHK' AND lifecycle='PLANNED' AND admin_code=420682`:                                           1,
		`SELECT count(*) FROM asset_devices WHERE id='LHK-CRANE-001' AND asset_status='DRAFT' AND rated_capacity_t IS NULL AND span_m IS NULL`: 1,
		`SELECT count(*) FROM asset_locations WHERE id='LHK-SITE' AND longitude IS NULL AND coordinate_system='UNKNOWN'`:                       1,
		`SELECT count(*) FROM asset_equipment_installations WHERE asset_id='LHK-CRANE-001' AND installed_at IS NULL AND serial_number IS NULL`: 51,
		`SELECT count(*) FROM onboarding_source_rows WHERE asset_id='LHK-CRANE-001'`:                                                           55,
		`SELECT count(*) FROM onboarding_source_rows WHERE row_kind='OPERATION' AND equipment_installation_id IS NULL`:                         4,
		`SELECT count(*) FROM telemetry_points WHERE confirmation_status='UNCONFIRMED' AND quality='UNVERIFIED'`:                               55,
		`SELECT count(*) FROM meter_definitions WHERE status='ACTIVE'`:                                                                         0,
		`SELECT count(*) FROM maintenance_rules WHERE approval_status<>'DRAFT_UNAPPROVED'`:                                                     0,
		`SELECT count(*) FROM asset_pending_attributes WHERE subject='asset' AND attribute IN ('ratedCapacityT','spanM') AND status='PENDING'`: 2,
		`SELECT count(*) FROM asset_pending_attributes WHERE attribute IN ('ropeCount','lidarDimension','rfidEntity') AND status='PENDING'`:    4,
		`SELECT count(*) FROM wire_rope_details WHERE rope_count IS NULL AND rope_count_status='UNCONFIRMED' AND nominal_diameter_mm=16`:       1,
		`SELECT count(*) FROM devices`: 0, // no runtime, no REAL identity
		`SELECT count(*) FROM audit_logs WHERE action='onboarding_applied' AND result='success'`:              1,
		`SELECT count(*) FROM entity_revisions WHERE entity_type='equipment' AND changed_by='host:ops-admin'`: 51,
	} {
		if got := n(t, db, q); got != want {
			t.Fatalf("%s = %d, want %d", q, got, want)
		}
	}
	// Lossless: every stored raw row equals the staging raw object.
	s, _ := Parse(b)
	for _, r := range s.Records {
		var raw string
		if err := db.QueryRow(`SELECT raw::text FROM onboarding_source_rows WHERE source_row=$1`, r.SourceRow).Scan(&raw); err != nil || !jsonEqual([]byte(raw), r.rawJSON) {
			t.Fatalf("row %d not lossless", r.SourceRow)
		}
	}
	rep, err := Verify(ctx, db, b)
	if err != nil || !rep.Consistent {
		t.Fatalf("verify %+v %v", rep.Actual, err)
	}
	// Re-apply: plan is all EXISTING, apply is a noop, nothing duplicated.
	p2, _, _ := PlanImport(ctx, db, b)
	rec2, err := Apply(ctx, db, b, p2.Digest, "ops-admin", "r2")
	if err != nil || rec2.Result != "noop" {
		t.Fatalf("re-apply %+v %v", rec2, err)
	}
	if n(t, db, `SELECT count(*) FROM asset_equipment_installations`) != 51 || n(t, db, `SELECT count(*) FROM equipment_catalog_items`) != p.New["catalog"] || n(t, db, `SELECT count(*) FROM projects WHERE id='LHK'`) != 1 {
		t.Fatal("re-apply duplicated rows")
	}
	// A changed staging document (different raw cell) is refused with a diff, nothing written.
	changed := bytes.Replace(b, []byte(`"DB505.DBX32.0"`), []byte(`"DB505.DBX32.7"`), 1)
	var doc map[string]any
	if json.Unmarshal(changed, &doc) != nil {
		t.Fatal("bad fixture")
	}
	p3, _, err := PlanImport(ctx, db, changed)
	if err != nil || len(p3.Conflicts) == 0 || p3.Conflicts[0].Kind != "source_row" {
		t.Fatalf("changed document not refused: %+v %v", p3.Conflicts, err)
	}
	if _, err = Apply(ctx, db, changed, p3.Digest, "ops-admin", "r3"); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting apply: %v", err)
	}
}

// Without the R9 equipment types the plan refuses (no silent wrong-category matching).
func TestImportRefusesUninitializedTypes(t *testing.T) {
	db := testsupport.Database(t)
	b, _ := Embedded()
	p, _, err := PlanImport(context.Background(), db, b)
	if err != nil {
		t.Fatal(err)
	}
	refused := 0
	for _, c := range p.Conflicts {
		if c.Kind == "equipment_type" {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("uninitialized types accepted")
	}
}
