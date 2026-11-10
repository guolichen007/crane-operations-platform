package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
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

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("%v: %s", err, q)
	}
}

// Real (non-UAT) development data that seed/cleanup must never touch.
func foreignRows(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING`,
		`INSERT INTO projects(id,tenant_id,name,lifecycle) VALUES ('P001','demo','other','PLANNED')`,
		`INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES ('X1','P001','x','PLANNED')`,
		`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES ('EDGE-X1','P001','X1','REAL')`,
		`INSERT INTO alarms(message_id,device_id,observed_at,payload_json) VALUES ('x-alarm','EDGE-X1',now(),'{}')`,
		`INSERT INTO users(id,subject,issuer,enabled) VALUES ('other','s-other','` + devIssuer + `',true)`,
		`INSERT INTO project_memberships(user_id,project_id,role) VALUES ('other','P001','viewer')`,
		// Look-alikes of the UAT namespace that the tool did NOT create: LIKE-based cleanup would
		// have deleted them; exact-id cleanup must keep them.
		`INSERT INTO projects(id,tenant_id,name,lifecycle) VALUES ('UAT-REAL-CUSTOMER','demo','真实项目恰好以 UAT- 开头','PLANNED')`,
		`INSERT INTO users(id,subject,issuer,enabled) VALUES ('uat-colleague','s-col','` + devIssuer + `',true)`,
		`INSERT INTO project_memberships(user_id,project_id,role) VALUES ('uat-colleague','UAT-REAL-CUSTOMER','viewer')`,
	} {
		mustExec(t, db, q)
	}
}

func TestSeedVerifyReseedCleanup(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	foreignRows(t, db)
	var out bytes.Buffer
	if err := seedAll(ctx, db, subjects(), devIssuer, time.Now().UTC(), &out); err != nil {
		t.Fatalf("seed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"UAT_DATA_VERIFY":"PASS"`) {
		t.Fatalf("verify not PASS: %s", out.String())
	}
	if n := count(t, db, `SELECT count(*) FROM alarms WHERE device_id LIKE 'UAT-%'`); n <= 100 {
		t.Fatalf("historical alarms must exceed 100, got %d", n)
	}
	// Re-seed is a deterministic reset, not a duplicate insert.
	out.Reset()
	if err := seedAll(ctx, db, subjects(), devIssuer, time.Now().UTC(), &out); err != nil {
		t.Fatalf("re-seed: %v\n%s", err, out.String())
	}
	if n := count(t, db, `SELECT count(*) FROM asset_devices WHERE project_id LIKE 'UAT-P%'`); n != len(assets()) {
		t.Fatalf("re-seed duplicated assets: %d", n)
	}
	out.Reset()
	if err := execute(ctx, db, "refresh", options{issuer: devIssuer}, &out); err != nil {
		t.Fatalf("refresh: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := execute(ctx, db, "cleanup", options{issuer: devIssuer}, &out); err == nil {
		t.Fatal("cleanup without --confirm-cleanup must be refused")
	}
	if err := execute(ctx, db, "cleanup", options{issuer: devIssuer, confirmCleanup: true}, &out); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM projects WHERE id LIKE 'UAT-P%'`:                       0,
		`SELECT count(*) FROM users WHERE id LIKE 'uat-%' AND id <> 'uat-colleague'`: 0,
		`SELECT count(*) FROM devices WHERE id LIKE 'UAT-%'`:                         0,
		`SELECT count(*) FROM tenants WHERE id='uat'`:                                0,
		`SELECT count(*) FROM projects WHERE id='P001'`:                              1,
		`SELECT count(*) FROM alarms WHERE message_id='x-alarm'`:                     1,
		`SELECT count(*) FROM project_memberships WHERE user_id='other'`:             1,
		`SELECT count(*) FROM projects WHERE id='UAT-REAL-CUSTOMER'`:                 1,
		`SELECT count(*) FROM users WHERE id='uat-colleague'`:                        1,
		`SELECT count(*) FROM project_memberships WHERE user_id='uat-colleague'`:     1,
		`SELECT count(*) FROM audit_logs WHERE user_id='host:uatdata'`:               2 * (len(users) + 8), // 6 binds + 8 grants per seed, two seeds; audit retained
	} {
		if got := count(t, db, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
}

// Ownership: anything attached to the UAT namespace that the tool did not create makes cleanup and
// re-seed refuse as a whole (nothing deleted), instead of silently removing it.
func TestCleanupRefusesForeignOwnership(t *testing.T) {
	for name, mutation := range map[string]string{
		"real account granted on UAT project": `INSERT INTO project_memberships(user_id,project_id,role) VALUES('other','UAT-P001','viewer')`,
		"UAT account granted on real project": `INSERT INTO project_memberships(user_id,project_id,role) VALUES('uat-admin','P001','viewer')`,
		"hand-made asset in UAT project":      `INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES('HANDMADE','UAT-P001','h','PLANNED')`,
		"project marker removed":              `UPDATE projects SET notes='' WHERE id='UAT-P003'`,
		"UAT user rebound to other issuer":    `UPDATE users SET issuer='https://elsewhere.example/realms/x' WHERE id='uat-zero'`,
		"location created in UAT project":     `INSERT INTO asset_locations(id,project_id,kind,name) VALUES('L-HAND','UAT-P002','SITE','hand')`,
	} {
		t.Run(name, func(t *testing.T) {
			db := testsupport.Database(t)
			ctx := context.Background()
			foreignRows(t, db)
			var out bytes.Buffer
			if err := seedAll(ctx, db, subjects(), devIssuer, time.Now().UTC(), &out); err != nil {
				t.Fatalf("seed: %v", err)
			}
			mustExec(t, db, mutation)
			before := count(t, db, `SELECT (SELECT count(*) FROM projects)+(SELECT count(*) FROM users)+(SELECT count(*) FROM asset_devices)+(SELECT count(*) FROM project_memberships)`)
			err := execute(ctx, db, "cleanup", options{issuer: devIssuer, confirmCleanup: true}, &out)
			if !errors.Is(err, ErrOwnership) {
				t.Fatalf("cleanup must refuse with ErrOwnership, got %v", err)
			}
			if err = seedAll(ctx, db, subjects(), devIssuer, time.Now().UTC(), &out); !errors.Is(err, ErrOwnership) {
				t.Fatalf("re-seed must refuse with ErrOwnership, got %v", err)
			}
			after := count(t, db, `SELECT (SELECT count(*) FROM projects)+(SELECT count(*) FROM users)+(SELECT count(*) FROM asset_devices)+(SELECT count(*) FROM project_memberships)`)
			if before != after {
				t.Fatalf("refused cleanup changed data: %d -> %d", before, after)
			}
		})
	}
}

// Counter-proof: the expectation table is not vacuous. Flipping A03 (REAL offline + SIM online)
// to REAL online must make verify fail on real.connected — i.e. verify really compares numbers.
func TestVerifyDetectsDrift(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := seedAll(ctx, db, subjects(), devIssuer, time.Now().UTC(), &out); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mustExec(t, db, `UPDATE device_shadow SET payload_json = jsonb_set(jsonb_set(payload_json,'{presence,connectivity}','"MQTT_CONNECTED"'),
	  '{presence,lastSeenAt}', to_jsonb((SELECT payload_json#>>'{presence,lastSeenAt}' FROM device_shadow WHERE device_id='UAT-EDGE-A01')))
	  WHERE device_id='UAT-EDGE-A03'`)
	res, err := verify(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Failures, "\n"), "uat-admin region=all PT24H metrics.real.connected: got 3 want 2") {
		t.Fatalf("drift not detected: %v", res.Failures)
	}
}

// Counter-proof for authorization: granting uat-admin P002 must change its numbers (isolation is
// enforced by data, not by the expectation table).
func TestVerifyDetectsGrantLeak(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := seedAll(ctx, db, subjects(), devIssuer, time.Now().UTC(), &out); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mustExec(t, db, `INSERT INTO project_memberships(user_id,project_id,role) VALUES('uat-admin','UAT-P002','viewer')`)
	res, err := verify(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Failures, "\n"), "uat-admin region=440000 PT24H metrics.projects: got 1 want 0") {
		t.Fatalf("grant leak not detected: %v", res.Failures)
	}
}

// reopen returns a fresh pool on the same isolated database so database-level settings
// (ALTER DATABASE ... SET) are visible to new sessions.
func reopen(t *testing.T, db *sql.DB) (*sql.DB, string) {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("A1_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	fresh, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.Close() })
	return fresh, name
}

func TestGuardRequiresServerSideIdentity(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	var sysid string
	if err := db.QueryRow(`SELECT system_identifier::text FROM pg_control_system()`).Scan(&sysid); err != nil {
		t.Fatal(err)
	}
	ok := options{issuer: devIssuer, systemID: sysid}
	_, name := reopen(t, db)
	// No marker yet: a production/unmarked database refuses even with every flag correct.
	if err := guard(ctx, db, ok, name); err == nil || !strings.Contains(err.Error(), envSetting) {
		t.Fatalf("unmarked database must be refused, got %v", err)
	}
	// A session-level value (e.g. injected through PGOPTIONS) must not satisfy the guard.
	spoof, _ := reopen(t, db)
	spoof.SetMaxOpenConns(1)
	mustExec(t, spoof, `SET crane.deployment_environment = 'crane-dev'`)
	if err := guard(ctx, spoof, ok, name); err == nil {
		t.Fatal("session-level marker must not satisfy the guard")
	}
	mustExec(t, db, `ALTER DATABASE "`+name+`" SET crane.deployment_environment = 'production'`)
	if err := guard(ctx, db, ok, name); err == nil {
		t.Fatal("marker other than crane-dev must be refused")
	}
	mustExec(t, db, `ALTER DATABASE "`+name+`" SET crane.deployment_environment = 'crane-dev'`)
	marked := db
	if err := guard(ctx, marked, ok, name); err != nil {
		t.Fatalf("valid crane-dev target refused: %v", err)
	}
	for label, o := range map[string]options{
		"preview issuer": {issuer: "https://b4.crane.youdaozd.com/realms/crane-preview", systemID: sysid},
		"trailing slash": {issuer: devIssuer + "/", systemID: sysid},
		"no system id":   {issuer: devIssuer},
		"other cluster":  {issuer: devIssuer, systemID: "1"},
	} {
		if err := guard(ctx, marked, o, name); err == nil {
			t.Errorf("%s: guard must refuse", label)
		}
	}
	if err := guard(ctx, marked, ok, "crane_app"); err == nil {
		t.Error("a different database name must be refused")
	}
	mustExec(t, db, `INSERT INTO users(id,subject,issuer,enabled) VALUES ('ext-user','s','https://elsewhere.example/realms/x',true)`)
	if err := guard(ctx, marked, ok, name); err == nil || !strings.Contains(err.Error(), "another issuer") {
		t.Fatalf("mixed-issuer database must be refused, got %v", err)
	}
}

func TestPlanNeedsNoDatabase(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"plan"}, &out); err != nil || !strings.Contains(out.String(), `"assets":148`) {
		t.Fatalf("plan: %v %s", err, out.String())
	}
	if err := run([]string{"seed", "--issuer", devIssuer}, &out); err == nil {
		t.Fatal("seed without database configuration must fail")
	}
}
