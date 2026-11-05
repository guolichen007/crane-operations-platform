package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatabaseG1Upgrade(t *testing.T) {
	parent := b2Database(t)
	ctx := context.Background()
	schema := "baobei_g1_" + hash(randomToken())[:16]
	g1Exec(t, parent, `CREATE SCHEMA `+schema)
	cfg := parent.Config().Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); g1Exec(t, parent, `DROP SCHEMA `+schema+` CASCADE`) })
	g1Exec(t, db, `CREATE TABLE baobei_migrations(name text PRIMARY KEY,checksum text NOT NULL)`)
	for _, name := range []string{"00001_init.sql", "00002_auth_and_governance.sql", "00003_staff_governance.sql"} {
		body, e := migrations.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		g1Exec(t, db, string(body))
		g1Exec(t, db, `INSERT INTO baobei_migrations VALUES($1,$2)`, name, fmt.Sprintf("%x", sha256.Sum256(body)))
	}
	if err = seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	raw := `{"projectName":"合成旧记录","estimatedAmount":0,"plannedEquipment":[{"quantity":2,"capacityTons":0,"spanM":0,"liftHeightM":0}]}`
	ids := []string{}
	for _, status := range []string{"DRAFT", "RETURNED", "SUBMITTED", "UNDER_REVIEW", "APPROVED", "REJECTED"} {
		var id string
		err = db.QueryRow(ctx, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,team_id,intake_status,document,estimated_amount,created_at,protected_until,next_review_at,version)
  VALUES('合成旧记录','synthetic','synthetic-a','synthetic-a',$1,$2,$3,0,'2025-01-01Z','2025-04-01Z','2025-02-01Z',7) RETURNING id::text`, syntheticTeam, status, raw).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	g1Exec(t, db, `INSERT INTO duplicate_cases(project_id,candidate_id,score,risk,decision) VALUES($1,$2,95,'HIGH','CONFIRMED_DUPLICATE')`, ids[5], ids[4])
	// Force a real failure after ALTER/UPDATE have run; the migrator must roll it all back.
	g1Exec(t, db, `CREATE TABLE project_submissions(collision integer)`)
	if err = migrate(ctx, db); err == nil {
		t.Fatal("migration failure not detected")
	}
	var found bool
	err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='intake_projects' AND column_name='draft_custodian_subject')`).Scan(&found)
	if err != nil || found {
		t.Fatal("partial migration", err)
	}
	var count int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM baobei_migrations`).Scan(&count)
	if count != 3 {
		t.Fatal("partial inventory")
	}
	g1Exec(t, db, `DROP TABLE project_submissions`)
	if err = migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db); err != nil {
		t.Fatal("not idempotent", err)
	}
	for _, id := range ids {
		p, e := get(ctx, db, id, false)
		if e != nil {
			t.Fatal(e)
		}
		var unchanged bool
		_ = db.QueryRow(ctx, `SELECT document=$2::jsonb AND version=7 AND created_at='2025-01-01Z'::timestamptz AND protected_until='2025-04-01Z'::timestamptz AND next_review_at='2025-02-01Z'::timestamptz FROM intake_projects WHERE id=$1`, id, raw).Scan(&unchanged)
		if !unchanged || p.Knowledge["estimatedAmount"] != "LEGACY_UNCONFIRMED" || p.EvidenceState != "INCOMPLETE" || p.SubmittedBySubject != "" || p.SubmissionRevision != 0 || p.DraftCustodianSubject != "synthetic-a" {
			t.Fatal("invented legacy facts")
		}
	}
	var complete, incomplete int
	_ = db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE evidence_state='COMPLETE'),count(*) FILTER(WHERE evidence_state='INCOMPLETE') FROM intake_projects`).Scan(&complete, &incomplete)
	if complete != 0 || incomplete != 6 {
		t.Fatal(complete, incomplete)
	}
	t.Logf("LEGACY_COMPLETE=%d LEGACY_INCOMPLETE=%d MIGRATION_ATOMIC_ROLLBACK=PASS", complete, incomplete)
	a := &App{db: db, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	a.cfg.Origin = server.URL
	mgr := login(t, server, "synthetic-manager")
	p, _ := get(ctx, db, ids[2], false)
	for _, action := range []string{"approve", "return", "reject", "duplicate-decisions"} {
		s, b := g1Decision(mgr, p, action, nil)
		expect(t, s, 409, b)
		if !bytes.Contains(b, []byte("REVIEW_EVIDENCE_INCOMPLETE")) {
			t.Fatal(string(b))
		}
	}
	// SQL constraints and immutable evidence are separate from HTTP guards.
	if _, err = db.Exec(ctx, `UPDATE intake_projects SET owner_subject=NULL WHERE id=$1`, ids[2]); err == nil {
		t.Fatal("NULL formal owner accepted")
	}
	if _, err = db.Exec(ctx, `UPDATE intake_projects SET owner_subject=NULL,team_id=NULL WHERE id=$1`, ids[0]); err == nil {
		t.Fatal("unassigned protected draft accepted")
	}
}

func TestDatabaseG1FirstProtection(t *testing.T) {
	f := g1FixtureFor(t)
	p := g1Create(t, f.sales, "合成延迟首次提交")
	g1Exec(t, f.db, `UPDATE intake_projects SET created_at=now()-interval '300 days' WHERE id=$1`, p.ID)
	p = g1Submit(t, f.sales, p)
	if p.ProtectionStartedAt == nil || p.ProtectedUntil == nil || p.ProtectionStatus != "ACTIVE" || time.Until(*p.ProtectedUntil) < 89*24*time.Hour {
		t.Fatal("draft consumed protection")
	}
	first := *p.ProtectionStartedAt
	s, b := g1Decision(f.manager, p, "return", nil)
	p = projectFrom(t, s, b)
	p = g1Submit(t, f.sales, p)
	if !p.ProtectionStartedAt.Equal(first) {
		t.Fatal("resubmission gifted protection")
	}
}

func TestDatabaseG1BackupAndOldSchemaGate(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	cfg := db.Config()
	schema := strings.Split(cfg.ConnConfig.RuntimeParams["search_path"], ",")[0]
	if cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" {
		t.Fatal("fixture must be loopback")
	}
	app := &App{db: db, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	sales := login(t, server, "synthetic-a")
	p := g1Create(t, sales, "合成备份提交证据")
	_ = g1Submit(t, sales, p)
	admin := login(t, server, "synthetic-admin")
	_ = g1Create(t, admin, "合成备份未分配草稿")
	docker := func(input []byte, args ...string) []byte {
		t.Helper()
		if c := os.Getenv("BAOBEI_DOCKER_CONTEXT"); c != "" {
			args = append([]string{"--context", c}, args...)
		}
		cmd := exec.Command("docker", args...)
		cmd.Stdin = bytes.NewReader(input)
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("isolated Docker database command failed: %v", err)
		}
		return b
	}
	ids := strings.Fields(string(docker(nil, "ps", "--filter", fmt.Sprintf("publish=%d", cfg.ConnConfig.Port), "-q")))
	if len(ids) != 1 {
		t.Fatal("exact disposable PostgreSQL container required", len(ids))
	}
	cid := ids[0]
	// Verify the published port belongs to this fixture, never choose a production connection.
	ports := string(docker(nil, "port", cid, "5432/tcp"))
	if !strings.Contains(ports, fmt.Sprintf(":%d", cfg.ConnConfig.Port)) {
		t.Fatal("fixture port mismatch")
	}
	serverVersion := string(docker(nil, "exec", cid, "psql", "-U", cfg.ConnConfig.User, "-d", cfg.ConnConfig.Database, "-Atc", "SHOW server_version"))
	if !strings.HasPrefix(serverVersion, "18.4") {
		t.Fatal("PostgreSQL 18.4 required")
	}
	restored := "baobei_g1_restore_" + hash(randomToken())[:16]
	dump := docker(nil, "exec", cid, "pg_dump", "-U", cfg.ConnConfig.User, "-d", cfg.ConnConfig.Database, "-Fc", "--no-owner", "--schema="+schema)
	docker(nil, "exec", cid, "createdb", "-U", cfg.ConnConfig.User, restored)
	defer docker(nil, "exec", cid, "dropdb", "-U", cfg.ConnConfig.User, restored)
	docker(nil, "exec", cid, "psql", "-U", cfg.ConnConfig.User, "-d", restored, "-v", "ON_ERROR_STOP=1", "-c", "CREATE EXTENSION pg_trgm")
	docker(dump, "exec", "-i", cid, "pg_restore", "-U", cfg.ConnConfig.User, "-d", restored, "--no-owner", "--exit-on-error")
	rc := cfg.Copy()
	rc.ConnConfig.Database = restored
	copyDB, err := pgxpool.NewWithConfig(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	if err = verifySchema(ctx, copyDB); err != nil {
		t.Fatal("restored schema", err)
	}
	for _, table := range []string{"app_users", "intake_projects", "project_submissions", "project_participations", "project_create_requests", "baobei_migrations"} {
		var before, after int
		if err = db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err = copyDB.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&after); err != nil || before != after {
			t.Fatal("restore count", table, err)
		}
	}
	for _, table := range []string{"intake_projects", "project_submissions", "project_participations", "project_create_requests"} {
		query := `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb)::text FROM ` + table + ` x`
		var before, after string
		if err = db.QueryRow(ctx, query).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err = copyDB.QueryRow(ctx, query).Scan(&after); err != nil || before != after {
			t.Fatal("restored evidence content mismatch", table, err)
		}
	}
	var hashes bool
	if err = copyDB.QueryRow(ctx, `SELECT bool_and(document_hash=encode(sha256(convert_to(document::text,'UTF8')),'hex')) FROM project_submissions`).Scan(&hashes); err != nil || !hashes {
		t.Fatal("frozen body hash mismatch", err)
	}
	// Compile the actual approved old verifySchema implementation with its three embedded SQL files.
	temp := t.TempDir()
	gitFile := func(path string) []byte {
		t.Helper()
		b, e := exec.Command("git", "show", "c580978b7056f95f1ac284f00b7d92f0b549f69b:"+path).Output()
		if e != nil {
			t.Fatal("old source unavailable", e)
		}
		return b
	}
	for _, name := range []string{"database.go", "go.mod", "go.sum", "migrations/00001_init.sql", "migrations/00002_auth_and_governance.sql", "migrations/00003_staff_governance.sql", "database-grants.sql"} {
		path := filepath.Join(temp, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, gitFile("services/project-intake-api/"+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	code := `package main
import("context";"os";"strings";"testing";"github.com/jackc/pgx/v5";"github.com/jackc/pgx/v5/pgxpool")
type querier interface{Query(context.Context,string,...any)(pgx.Rows,error);QueryRow(context.Context,string,...any)pgx.Row}
func TestActualOldInventoryRejects(t *testing.T){ctx:=context.Background();c,e:=pgxpool.ParseConfig(os.Getenv("BAOBEI_OLD_GATE_DSN"));if e!=nil{t.Fatal(e)};c.ConnConfig.RuntimeParams["search_path"]=os.Getenv("BAOBEI_OLD_GATE_SCHEMA")+",public";db,e:=pgxpool.NewWithConfig(ctx,c);if e!=nil{t.Fatal(e)};defer db.Close();e=verifySchema(ctx,db);if e==nil||!strings.Contains(e.Error(),"inventory mismatch"){t.Fatal("old schema gate did not reject",e)}}`
	if err = os.WriteFile(filepath.Join(temp, "old_gate_test.go"), []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	goTool := os.Getenv("BAOBEI_GO")
	if goTool == "" {
		goTool = "go"
	}
	cmd := exec.Command(goTool, "test", "-mod=readonly", "-run", "TestActualOldInventoryRejects", "-count=1", "./...")
	cmd.Dir = temp
	cmd.Env = append(os.Environ(), "BAOBEI_OLD_GATE_DSN="+rc.ConnConfig.ConnString(), "BAOBEI_OLD_GATE_SCHEMA="+schema)
	if _, err = cmd.Output(); err != nil {
		t.Fatal("actual old gate compilation/check failed", err)
	}
	t.Log("PG18_4_BACKUP_RESTORE=PASS ACTUAL_OLD_SCHEMA_GATE=DENIED")
}
