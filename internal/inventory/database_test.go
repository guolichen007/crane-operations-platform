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
func TestLegacyResetHasEnvironmentAndExactIdentityProtection(t *testing.T) {
	db := database(t)
	migrateDB(t, db, 3)
	ctx := context.Background()
	if _, e := db.Exec(`INSERT INTO devices(id,project_id,source_mode) SELECT 'SIM-'||lpad(i::text,3,'0'),'Project-A','SIMULATION' FROM generate_series(1,21) i; INSERT INTO devices(id,project_id,source_mode) VALUES('TEST-001','Project-A','TEST')`); e != nil {
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
	if count(t, db, `SELECT count(*) FROM devices`) != 2 || count(t, db, `SELECT count(*) FROM devices WHERE id IN ('SIM-021','TEST-001')`) != 2 {
		t.Fatal("reset exceeded original20")
	}
}
