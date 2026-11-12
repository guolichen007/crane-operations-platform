package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 00007 → 00008 on a database with existing data: a schema built from
// 00001–00007, populated, then upgraded by the normal migrator.
func TestDatabaseR2P1MigrationFrom00007(t *testing.T) {
	db := b2Database(t) // full schema; rebuild a fresh 00007 schema next to it
	ctx := context.Background()
	old7 := "r2p1_old_" + hash(randomToken())[:12]
	if _, err := db.Exec(ctx, `CREATE SCHEMA `+old7); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DROP SCHEMA `+old7+` CASCADE`) })
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, `SET search_path=`+old7+`,public`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `CREATE TABLE baobei_migrations(name text PRIMARY KEY, checksum text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for _, e := range entries {
		if e.Name() >= "00008" {
			break
		}
		b, _ := migrations.ReadFile("migrations/" + e.Name())
		if _, err = conn.Exec(ctx, string(b)); err != nil {
			t.Fatal(e.Name(), err)
		}
		h := sha256.Sum256(b)
		if _, err = conn.Exec(ctx, `INSERT INTO baobei_migrations VALUES($1,$2)`, e.Name(), hex.EncodeToString(h[:])); err != nil {
			t.Fatal(err)
		}
	}
	// Existing data under 00007: a team, a bound member with history, a pending
	// member, a draft and a submitted project.
	for _, sql := range []string{
		`INSERT INTO teams(id,name) VALUES('33333333-3333-4333-8333-333333333333','旧团队')`,
		`INSERT INTO app_users(subject,display_name,role,team_id,staff_managed,staff_status,active,account_state) VALUES
		 ('aaaaaaaa-0000-4000-8000-000000000001','旧销售','baobei_sales','33333333-3333-4333-8333-333333333333',true,'ACTIVE',true,'READY')`,
		`INSERT INTO app_users(staff_id,subject,display_name,role,team_id,staff_managed,staff_status,active) VALUES
		 ('bbbbbbbb-0000-4000-8000-000000000002','pending:bbbbbbbb-0000-4000-8000-000000000002','旧待开通','baobei_sales','33333333-3333-4333-8333-333333333333',true,'PENDING_PROVISION',false)`,
		`INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,document)
		 VALUES('旧草稿','旧草稿','aaaaaaaa-0000-4000-8000-000000000001','aaaaaaaa-0000-4000-8000-000000000001','aaaaaaaa-0000-4000-8000-000000000001','33333333-3333-4333-8333-333333333333','{}')`,
	} {
		if _, err = conn.Exec(ctx, sql); err != nil {
			t.Fatal(sql, err)
		}
	}
	// Upgrade with the real migrator (same function the API runs at start).
	cfg := db.Config().Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = old7 + ",public"
	old, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err = migrate(ctx, old); err != nil {
		t.Fatal("upgrade 00007→00008", err)
	}
	if err = verifySchema(ctx, old); err != nil {
		t.Fatal(err)
	}
	var teamStatus, projectStatus string
	var discarded *string
	if err = old.QueryRow(ctx, `SELECT status FROM teams WHERE name='旧团队'`).Scan(&teamStatus); err != nil || teamStatus != "ACTIVE" {
		t.Fatal("existing team", teamStatus, err)
	}
	if err = old.QueryRow(ctx, `SELECT intake_status,discarded_at::text FROM intake_projects WHERE project_name='旧草稿'`).Scan(&projectStatus, &discarded); err != nil || projectStatus != "DRAFT" || discarded != nil {
		t.Fatal("existing draft", projectStatus, err)
	}
	// The bound member with history keeps the subject (new guard on old data).
	if _, err = old.Exec(ctx, `UPDATE app_users SET account_state='REVIEW' WHERE display_name='旧销售'`); err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(ctx, `UPDATE app_users SET subject='pending:'||staff_id::text WHERE display_name='旧销售'`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "BB002" {
		t.Fatal("subject with project history moved back after upgrade", err)
	}
	// Second run is a no-op.
	if err = migrate(ctx, old); err != nil {
		t.Fatal("re-run", err)
	}
}
