package inventory

import (
	"context"
	"database/sql"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/deploy/postgres/migrations"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"net/url"
	"os"
	"testing"
	"time"
)

func database(t *testing.T) *sql.DB {
	t.Helper()
	source := os.Getenv("A0_TEST_DATABASE_URL")
	if source == "" {
		t.Skip("real PostgreSQL assertions run in Compose with A0_TEST_DATABASE_URL")
	}
	root, e := sql.Open("pgx", source)
	if e != nil {
		t.Fatal(e)
	}
	name := "crane_a0_test_" + platform.ID()
	if !contract.ValidID(name) {
		t.Fatal("invalid test db")
	}
	if _, e = root.Exec(`CREATE DATABASE "` + name + `"`); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(source)
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	db, e := sql.Open("pgx", u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Close()
		_, err := root.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`)
		root.Close()
		if err != nil {
			t.Error("test database cleanup", err)
		}
	})
	return db
}
func migrateDB(t *testing.T, db *sql.DB, target int64) {
	t.Helper()
	goose.SetLogger(goose.NopLogger())
	goose.SetBaseFS(migrations.Files)
	if e := goose.SetDialect("postgres"); e != nil {
		t.Fatal(e)
	}
	if e := goose.UpToContext(context.Background(), db, ".", target); e != nil {
		t.Fatal(e)
	}
}
func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if e := db.QueryRow(query).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestMigrationEmptyAndVersion2Upgrade(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "v2_upgrade"}[upgrade], func(t *testing.T) {
			db := database(t)
			if upgrade {
				migrateDB(t, db, 2)
				if _, e := db.Exec(`INSERT INTO devices(id,project_id) VALUES('SIM-001','Project-A'); INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES('SIM-001','{"reported":{"load":4}}',now(),7)`); e != nil {
					t.Fatal(e)
				}
			}
			migrateDB(t, db, 3)
			if v, e := goose.GetDBVersion(db); e != nil || v != 3 {
				t.Fatal("migration version", v, e)
			}
			if upgrade {
				if count(t, db, `SELECT count(*) FROM devices d JOIN device_shadow s ON s.device_id=d.id WHERE d.source_mode='SIMULATION' AND d.asset_device_id IS NULL AND s.version=7 AND s.payload_json#>>'{reported,load}'='4'`) != 1 {
					t.Fatal("upgrade lost old runtime/shadow")
				}
			}
		})
	}
}
func TestImportIdempotentRollbackAndRealIdentitySeparation(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 3)
	d := example(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if e := Import(ctx, db, d); e != nil {
			t.Fatal(e)
		}
	}
	if count(t, db, `SELECT count(*) FROM projects`) != 9 || count(t, db, `SELECT count(*) FROM asset_devices`) != 10 || count(t, db, `SELECT count(*) FROM devices WHERE source_mode='SIMULATION'`) != 10 || count(t, db, `SELECT count(*) FROM devices WHERE source_mode='REAL'`) != 0 {
		t.Fatal("non-idempotent inventory")
	}
	p, e := Projects(ctx, db)
	if e != nil || len(p) != 9 {
		t.Fatal("project API", e)
	}
	a, e := Assets(ctx, db)
	if e != nil || len(a) != 10 || a[0].CraneModel != nil || a[0].Connectivity != nil {
		t.Fatal("asset API invented missing metadata", e)
	}
	if count(t, db, `SELECT count(*) FROM asset_devices WHERE project_id IN ('P007','P008','P009')`) != 0 {
		t.Fatal("invented completed assets")
	}
	bad := clone(d)
	bad.Projects[0].Name = "Changed"
	bad.RuntimeDevices[0].SourceMode = "TEST"
	if Import(ctx, db, bad) == nil {
		t.Fatal("runtime source reused")
	}
	var name string
	if e = db.QueryRow(`SELECT name FROM projects WHERE id='P001'`).Scan(&name); e != nil || name != "Project-A" {
		t.Fatal("failed import partially committed")
	}
	real := d.RuntimeDevices[0]
	real.RuntimeDeviceId = "EDGE-P001-01"
	real.SourceMode = "REAL"
	d.RuntimeDevices = append(d.RuntimeDevices, real)
	if e = Import(ctx, db, d); e != nil {
		t.Fatal("separate real identity rejected", e)
	}
	if count(t, db, `SELECT count(*) FROM devices WHERE asset_device_id='ASSET-P001-01'`) != 2 {
		t.Fatal("simulation history replaced by real identity")
	}
}

// Inventory ownership counterproof: first import seeds BUSINESS_OWNED; a later
// import of the same document must not overwrite a business edit, and runtime
// identity/provenance stays immutable.
func TestImportDoesNotOverwriteExistingBusinessFields(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 6)
	ctx := context.Background()
	d := example(t)
	if e := Import(ctx, db, d); e != nil {
		t.Fatal(e)
	}
	// Simulate a business edit through the business path (direct UPDATE here).
	if _, e := db.Exec(`UPDATE projects SET name='Business-Edited' WHERE id='P001'`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE asset_devices SET display_name='Business-Asset', crane_model='Business-Crane' WHERE id='ASSET-P001-01'`); e != nil {
		t.Fatal(e)
	}
	// Re-import the exact same OLD document.
	if e := Import(ctx, db, d); e != nil {
		t.Fatal(e)
	}
	var name, display, model string
	if e := db.QueryRow(`SELECT name FROM projects WHERE id='P001'`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	if name != "Business-Edited" {
		t.Fatalf("project BUSINESS name was overwritten: %q", name)
	}
	if e := db.QueryRow(`SELECT display_name,crane_model FROM asset_devices WHERE id='ASSET-P001-01'`).Scan(&display, &model); e != nil {
		t.Fatal(e)
	}
	if display != "Business-Asset" || model != "Business-Crane" {
		t.Fatalf("asset BUSINESS fields were overwritten: %q / %q", display, model)
	}
	// Runtime identity and provenance remain immutable.
	if count(t, db, `SELECT count(*) FROM devices WHERE asset_device_id='ASSET-P001-01' AND source_mode='SIMULATION'`) != 1 {
		t.Fatal("runtime provenance changed after re-import")
	}
}

func TestFreshMigrationCreatesR2Foundation(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 6)
	for _, table := range []string{
		"equipment_types", "manufacturers", "asset_locations", "asset_installations",
		"equipment_catalog_items", "asset_equipment_installations", "equipment_relations",
		"entity_revisions", "asset_timeline_events", "equipment_runtime_bindings",
	} {
		if count(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='`+table+`'`) != 1 {
			t.Fatalf("foundation table %s missing", table)
		}
	}
	for _, col := range []string{"source_sent_at", "cloud_received_at", "boot_id", "boot_seq", "sequence", "quality_flags"} {
		if count(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_name='telemetry_samples' AND column_name='`+col+`'`) != 1 {
			t.Fatalf("telemetry_samples column %s missing", col)
		}
	}
	if count(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_name LIKE 'component_%'`) != 0 {
		t.Fatal("component_* tables created")
	}
}

func TestLegacyResetHasEnvironmentAndExactIdentityProtection(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 3)
	ctx := context.Background()
	if _, e := db.Exec(`INSERT INTO devices(id,project_id,source_mode) SELECT 'SIM-'||lpad(i::text,3,'0'),'Project-A','SIMULATION' FROM generate_series(1,21) i; INSERT INTO devices(id,project_id,source_mode) VALUES('TEST-001','Project-A','TEST')`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`
INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES('SIM-001','{}',now(),1),('SIM-021','{}',now(),1);
INSERT INTO device_heartbeats VALUES('SIM-001',now());
INSERT INTO telemetry_samples(device_id,sampled_at,payload_json) VALUES('SIM-001',now(),'{}');
INSERT INTO alarms VALUES('legacy-alarm','SIM-001',now(),'{}');
INSERT INTO events VALUES('legacy-event','SIM-001',now(),'{}');
INSERT INTO live_sessions VALUES('legacy-viewer','SIM-001','dev-admin',now()+interval '1 minute');
INSERT INTO remote_sessions(session_id,device_id,user_id,expires_at,idle_seconds,audit_id) VALUES('legacy-disabled-remote','SIM-001','dev-admin',now(),0,'audit-retained');
INSERT INTO device_live_state(device_id,lease_id,generation,status,desired_until,updated_at) VALUES('SIM-001','legacy-upstream',1,'SILENT',now(),now());
INSERT INTO device_commands(command_id,device_id,kind,generation,status,created_at,expires_at,payload_json) VALUES('legacy-command','SIM-001','WATCH_START',1,'EXPIRED',now(),now(),'{}');
INSERT INTO audit_logs(user_id,device_id,action,result) VALUES('dev-admin','SIM-001','legacy-fixture','retained');
`); e != nil {
		t.Fatal(e)
	}
	if ResetLegacy(ctx, db, "production") == nil {
		t.Fatal("production reset accepted")
	}
	if count(t, db, `SELECT count(*) FROM devices`) != 22 {
		t.Fatal("rejected reset changed rows")
	}
	if e := ResetLegacy(ctx, db, "development"); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"device_commands", "device_live_state", "live_sessions", "remote_sessions", "alarms", "events", "telemetry_samples", "device_heartbeats"} {
		if count(t, db, "SELECT count(*) FROM "+table) != 0 {
			t.Fatal("legacy child rows retained", table)
		}
	}
	if count(t, db, `SELECT count(*) FROM device_shadow WHERE device_id='SIM-021'`) != 1 || count(t, db, `SELECT count(*) FROM audit_logs`) != 1 {
		t.Fatal("nonlegacy shadow or audit was deleted")
	}
	if count(t, db, `SELECT count(*) FROM devices`) != 2 || count(t, db, `SELECT count(*) FROM devices WHERE id IN ('SIM-021','TEST-001')`) != 2 {
		t.Fatal("reset exceeded original20")
	}
}

func seedTwoProjects(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, e := db.Exec(`INSERT INTO projects(id,tenant_id,name) VALUES('P-A','demo','A'),('P-B','demo','B')`); e != nil {
		t.Fatal(e)
	}
}

func TestAssetCodeProjectUnique(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 6)
	seedTwoProjects(t, db)
	// Same asset_code in different projects is allowed.
	if _, e := db.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_code) VALUES('ASSET-A1','P-A','A1','PLANNED','QD-001'),('ASSET-B1','P-B','B1','PLANNED','QD-001')`); e != nil {
		t.Fatal("same asset_code in different projects rejected", e)
	}
	// Duplicate asset_code within the same project is rejected.
	if _, e := db.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_code) VALUES('ASSET-A2','P-A','A2','PLANNED','QD-001')`); e == nil {
		t.Fatal("duplicate asset_code in same project accepted")
	}
}

func TestLocationParentProjectInvariant(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 6)
	seedTwoProjects(t, db)
	if _, e := db.Exec(`INSERT INTO asset_locations(id,project_id,kind,name) VALUES('LOC-A1','P-A','SITE','A1'),('LOC-B1','P-B','BAY','B1')`); e != nil {
		t.Fatal(e)
	}
	// A child location in project B must not point at a parent in project A.
	if _, e := db.Exec(`INSERT INTO asset_locations(id,project_id,parent_id,kind,name) VALUES('LOC-B2','P-B','LOC-A1','BAY','B2')`); e == nil {
		t.Fatal("cross-project location parent accepted")
	}
}

func seedTwoAssetsAndEquipment(t *testing.T, db *sql.DB) {
	t.Helper()
	seedTwoProjects(t, db)
	if _, e := db.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES('ASSET-A1','P-A','A1','PLANNED'),('ASSET-B1','P-B','B1','PLANNED')`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain) VALUES('MOTOR','Motor','MECHANICAL')`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO asset_equipment_installations(id,asset_id,equipment_code,type_code,valid_from) VALUES('EQ-A1','ASSET-A1','M-1','MOTOR',now()),('EQ-B1','ASSET-B1','M-1','MOTOR',now())`); e != nil {
		t.Fatal(e)
	}
}

func TestEquipmentRelationSameAssetInvariant(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 6)
	seedTwoAssetsAndEquipment(t, db)
	// from/asset and to/asset must be consistent; a cross-asset relation is rejected.
	if _, e := db.Exec(`INSERT INTO equipment_relations(id,asset_id,from_equipment_installation_id,to_equipment_installation_id,relation_type,valid_from) VALUES('REL-1','ASSET-A1','EQ-A1','EQ-B1','DRIVES',now())`); e == nil {
		t.Fatal("cross-asset equipment relation accepted")
	}
}

func TestRuntimeBindingSameAssetInvariant(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 6)
	seedTwoAssetsAndEquipment(t, db)
	// A runtime binding must not pair an asset with another asset's equipment.
	if _, e := db.Exec(`INSERT INTO equipment_runtime_bindings(id,asset_id,equipment_installation_id,source_kind,source_ref,valid_from) VALUES('RB-1','ASSET-A1','EQ-B1','MQTT','ref',now())`); e == nil {
		t.Fatal("cross-asset runtime binding accepted")
	}
}

func TestAssetInstallationLocationProjectInvariant(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 6)
	seedTwoProjects(t, db)
	if _, e := db.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES('ASSET-A1','P-A','A1','PLANNED')`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO asset_locations(id,project_id,kind,name) VALUES('LOC-B1','P-B','SITE','B1')`); e != nil {
		t.Fatal(e)
	}
	// An asset must never be installed at another project's location.
	if _, e := db.Exec(`INSERT INTO asset_installations(id,asset_id,location_id,valid_from) VALUES('INST-1','ASSET-A1','LOC-B1',now())`); e == nil {
		t.Fatal("cross-project asset installation accepted")
	}
}

func TestLegacyTelemetryQualityStaysNull(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 6)
	if _, e := db.Exec(`INSERT INTO devices(id,project_id,source_mode) VALUES('SIM-001','Project-A','SIMULATION')`); e != nil {
		t.Fatal(e)
	}
	// Pre-00006 three-column insert: time-quality columns stay NULL, never a fake GOOD.
	if _, e := db.Exec(`INSERT INTO telemetry_samples(device_id,sampled_at,payload_json) VALUES('SIM-001',now(),'{}')`); e != nil {
		t.Fatal(e)
	}
	var sourceSent, received *time.Time
	var bootID, qualityState *string
	var qualityFlags *[]string
	if e := db.QueryRow(`SELECT source_sent_at,cloud_received_at,boot_id,quality_state,quality_flags FROM telemetry_samples WHERE device_id='SIM-001'`).Scan(&sourceSent, &received, &bootID, &qualityState, &qualityFlags); e != nil {
		t.Fatal(e)
	}
	if sourceSent != nil || received != nil || bootID != nil || qualityState != nil || qualityFlags != nil {
		t.Fatal("legacy telemetry fabricated time-quality metadata")
	}
}
