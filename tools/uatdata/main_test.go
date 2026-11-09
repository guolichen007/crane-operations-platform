package main

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func subjects() map[string]string {
	m := map[string]string{}
	for _, u := range users {
		m[u.ID] = "kc-sub-" + u.ID
	}
	return m
}

func count(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Unrelated rows that seed/cleanup must never touch (stand-ins for any non-UAT Preview data).
func foreignRows(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING`,
		`INSERT INTO projects(id,tenant_id,name,lifecycle) VALUES ('P001','demo','other','PLANNED')`,
		`INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES ('X1','P001','x','PLANNED')`,
		`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES ('EDGE-X1','P001','X1','REAL')`,
		`INSERT INTO alarms(message_id,device_id,observed_at,payload_json) VALUES ('x-alarm','EDGE-X1',now(),'{}')`,
		`INSERT INTO users(id,subject,issuer,enabled) VALUES ('other','s-other','` + previewIssuer + `',true)`,
		`INSERT INTO project_memberships(user_id,project_id,role) VALUES ('other','P001','viewer')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v: %s", err, q)
		}
	}
}

func TestSeedVerifyReseedCleanup(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	foreignRows(t, db)
	var out bytes.Buffer
	if err := seedAll(ctx, db, subjects(), previewIssuer, time.Now().UTC(), &out); err != nil {
		t.Fatalf("seed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"UAT_DATA_VERIFY":"PASS"`) {
		t.Fatalf("verify not PASS: %s", out.String())
	}
	if n := count(t, db, `SELECT count(*) FROM alarms WHERE device_id IN (SELECT id FROM devices WHERE project_id LIKE 'UAT-%')`); n <= 100 {
		t.Fatalf("historical alarms must exceed 100, got %d", n)
	}
	// Re-seed is a deterministic reset, not a duplicate insert.
	out.Reset()
	if err := seedAll(ctx, db, subjects(), previewIssuer, time.Now().UTC(), &out); err != nil {
		t.Fatalf("re-seed: %v\n%s", err, out.String())
	}
	if n := count(t, db, `SELECT count(*) FROM asset_devices WHERE project_id LIKE 'UAT-%'`); n != len(assets()) {
		t.Fatalf("re-seed duplicated assets: %d", n)
	}
	// Refresh moves the anchor and keeps every expectation true.
	out.Reset()
	if err := execute(ctx, db, "refresh", options{}, &out); err != nil {
		t.Fatalf("refresh: %v\n%s", err, out.String())
	}
	// Cleanup removes exactly the UAT namespace.
	out.Reset()
	if err := execute(ctx, db, "cleanup", options{}, &out); err == nil {
		t.Fatal("cleanup without --confirm-cleanup must be refused")
	}
	if err := execute(ctx, db, "cleanup", options{confirmCleanup: true}, &out); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM projects WHERE id LIKE 'UAT-%'`:            0,
		`SELECT count(*) FROM users WHERE id LIKE 'uat-%'`:               0,
		`SELECT count(*) FROM devices WHERE project_id LIKE 'UAT-%'`:     0,
		`SELECT count(*) FROM tenants WHERE id='uat'`:                    0,
		`SELECT count(*) FROM projects WHERE id='P001'`:                  1,
		`SELECT count(*) FROM alarms WHERE message_id='x-alarm'`:         1,
		`SELECT count(*) FROM project_memberships WHERE user_id='other'`: 1,
		`SELECT count(*) FROM audit_logs WHERE user_id='host:uatdata'`:   2 * (len(users) + 8), // 6 binds + 8 grants per seed, two seeds; audit retained
	} {
		if got := count(t, db, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
}

// Counter-proof: the expectation table is not vacuous. Flipping A03 (REAL offline + SIM online)
// to REAL online must make verify fail on real.connected — i.e. verify really compares numbers.
func TestVerifyDetectsDrift(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := seedAll(ctx, db, subjects(), previewIssuer, time.Now().UTC(), &out); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`UPDATE device_shadow SET payload_json = jsonb_set(jsonb_set(payload_json,'{presence,connectivity}','"MQTT_CONNECTED"'),
	  '{presence,lastSeenAt}', to_jsonb((SELECT payload_json#>>'{presence,lastSeenAt}' FROM device_shadow WHERE device_id='UAT-EDGE-A01')))
	  WHERE device_id='UAT-EDGE-A03'`); err != nil {
		t.Fatal(err)
	}
	res, err := verify(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range res.Failures {
		if strings.Contains(f, "uat-admin region=all PT24H metrics.real.connected: got 3 want 2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("drift not detected: %v", res.Failures)
	}
}

// Counter-proof for authorization: granting uat-admin P002 must change its numbers (isolation is
// enforced by data, not by the expectation table).
func TestVerifyDetectsGrantLeak(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := seedAll(ctx, db, subjects(), previewIssuer, time.Now().UTC(), &out); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO project_memberships(user_id,project_id,role) VALUES('uat-admin','UAT-P002','viewer')`); err != nil {
		t.Fatal(err)
	}
	res, err := verify(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failures) == 0 || !strings.Contains(strings.Join(res.Failures, "\n"), "uat-admin region=440000 PT24H metrics.projects: got 1 want 0") {
		t.Fatalf("grant leak not detected: %v", res.Failures)
	}
}

func TestGuardRefusesNonPreview(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	var sysid string
	if err := db.QueryRow(`SELECT system_identifier::text FROM pg_control_system()`).Scan(&sysid); err != nil {
		t.Fatal(err)
	}
	ok := options{issuer: previewIssuer, systemID: sysid}
	if err := guard(ctx, db, ok); err != nil {
		t.Fatalf("valid Preview guard refused: %v", err)
	}
	for name, o := range map[string]options{
		"production issuer": {issuer: "https://crane.youdaozd.com/realms/crane", systemID: sysid},
		"trailing slash":    {issuer: previewIssuer + "/", systemID: sysid},
		"no system id":      {issuer: previewIssuer},
		"other cluster":     {issuer: previewIssuer, systemID: "1"},
	} {
		if err := guard(ctx, db, o); err == nil {
			t.Errorf("%s: guard must refuse", name)
		}
	}
	if _, err := db.Exec(`INSERT INTO users(id,subject,issuer,enabled) VALUES ('prod-user','s','https://crane.youdaozd.com/realms/crane',true)`); err != nil {
		t.Fatal(err)
	}
	if err := guard(ctx, db, ok); err == nil || !strings.Contains(err.Error(), "non-Preview issuer") {
		t.Fatalf("mixed-issuer database must be refused, got %v", err)
	}
}

func TestPlanNeedsNoDatabase(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"plan"}, &out); err != nil || !strings.Contains(out.String(), `"assets":148`) {
		t.Fatalf("plan: %v %s", err, out.String())
	}
	if err := run([]string{"seed", "--issuer", previewIssuer}, &out); err == nil {
		t.Fatal("seed without database configuration must fail")
	}
}
