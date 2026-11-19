package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// The fixture seed itself runs on PostgreSQL: production import, SIMULATION runtime registration,
// the reviewed fixture batches and the test-only rule reaching DUE.
func TestSeedLHKOnIsolatedDatabase(t *testing.T) {
	db := testsupport.Database(t)
	for _, stmt := range []string{
		`INSERT INTO tenants(id,name) VALUES('demo','demo') ON CONFLICT DO NOTHING`,
		`INSERT INTO users(id,subject,issuer,display_name,enabled) VALUES('b4-admin','s1','https://x','a',true),('b4-maintainer','s2','https://x','m',true),('b4-viewer','s3','https://x','v',true)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	saved := openFixtureDB
	t.Cleanup(func() { openFixtureDB = saved })
	// seedLHK closes its handle; keep the test database open by handing out a fresh pool each time.
	openFixtureDB = func() (*sql.DB, error) { return sql.Open("pgx", testsupport.URL(t, db)) }
	if err := seedLHK(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM maintenance_alerts WHERE source_mode='SIMULATION' AND stage='PCT_100'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("PCT_100 alert %d %v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM meter_states WHERE source_mode='SIMULATION'`).Scan(&n); err != nil || n != 57 {
		t.Fatalf("meters %d %v", n, err)
	}
}
