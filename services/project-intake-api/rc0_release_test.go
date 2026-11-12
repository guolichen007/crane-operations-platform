package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// G5-RC0, against real PostgreSQL 18.4. These tests prove why an upgrade across
// 00006 needs a maintenance window and that the identity tooling fails closed.

// oldAPISessionInsert is the session INSERT of the API before 00006 (253b4a8,
// auth.go): it does not know the issuer / identity_epoch columns.
const oldAPISessionInsert = `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at) VALUES($1,$2,$3,now()+interval '8 hours')`

// schemaUpTo builds a fresh schema from migrations strictly before `stop`.
func schemaUpTo(t *testing.T, stop string) *pgxpool.Pool {
	t.Helper()
	db := b2Database(t) // full schema (for the cleanup and DSN); build another next to it
	ctx := context.Background()
	name := "rc0_" + hash(randomToken())[:12]
	if _, err := db.Exec(ctx, `CREATE SCHEMA `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DROP SCHEMA `+name+` CASCADE`) })
	cfg := db.Config().Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = name + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(ctx, `CREATE TABLE baobei_migrations(name text PRIMARY KEY, checksum text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for _, e := range entries {
		if stop != "" && e.Name() >= stop {
			break
		}
		b, _ := migrations.ReadFile("migrations/" + e.Name())
		if _, err = pool.Exec(ctx, string(b)); err != nil {
			t.Fatal(e.Name(), err)
		}
		h := sha256.Sum256(b)
		if _, err = pool.Exec(ctx, `INSERT INTO baobei_migrations VALUES($1,$2)`, e.Name(), hex.EncodeToString(h[:])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO app_users(subject,display_name,role) VALUES('synthetic-old','旧版销售','baobei_sales')`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestDatabaseRC0MixedVersionNeedsMaintenanceWindow(t *testing.T) {
	ctx := context.Background()
	// 1. Old API + old schema (00001–00005): sign-in works.
	old := schemaUpTo(t, "00006")
	if _, err := old.Exec(ctx, oldAPISessionInsert, "tok-old-"+randomToken(), "synthetic-old", "csrf"); err != nil {
		t.Fatal("old API on old schema must sign in", err)
	}
	// 2. Old API + new schema (after 00006): the session cannot be created.
	if err := migrate(ctx, old); err != nil {
		t.Fatal("upgrade", err)
	}
	_, err := old.Exec(ctx, oldAPISessionInsert, "tok-mixed-"+randomToken(), "synthetic-old", "csrf")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" {
		t.Fatal("old API on the new schema must fail (NOT NULL issuer/identity_epoch)", err)
	}
	// Sessions created by the old API before the upgrade are invalid afterwards:
	// they carry the 'legacy:unknown' issuer and epoch 0.
	var legacy int
	_ = old.QueryRow(ctx, `SELECT count(*) FROM app_sessions WHERE issuer='legacy:unknown' AND identity_epoch=0`).Scan(&legacy)
	if legacy != 1 {
		t.Fatal("pre-upgrade session not marked legacy", legacy)
	}
	// 3. New API without an identity epoch: refuses to start (deployed env).
	issuer := deployedProfiles["production"].Realms["crane"].Issuer
	if err = verifyIdentityEpoch(ctx, old, "production", issuer); !errors.Is(err, errIdentityEpochMismatch) {
		t.Fatal("new API must refuse without an identity epoch", err)
	}
	// 4. Matching epoch: the new API's session insert works.
	if _, _, _, err = switchIdentity(ctx, old, issuer, "rc0-operator", "维护窗口内建立身份纪元"); err != nil {
		t.Fatal(err)
	}
	if err = verifyIdentityEpoch(ctx, old, "production", issuer); err != nil {
		t.Fatal("matching issuer", err)
	}
	tag, err := old.Exec(ctx, `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at,issuer,identity_epoch)
	 SELECT $1,'synthetic-old','csrf',now()+interval '8 hours',i.issuer,i.epoch FROM identity_epoch i WHERE i.issuer=$2`, "tok-new-"+randomToken(), issuer)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("new API sign-in", err)
	}
	// A new API configured for the other realm still refuses.
	if err = verifyIdentityEpoch(ctx, old, "production", deployedProfiles["production"].Realms["baobei"].Issuer); !errors.Is(err, errIdentityEpochMismatch) {
		t.Fatal("wrong issuer accepted", err)
	}
}

// identity-switch --dry-run must never report success on a schema it cannot read.
func TestDatabaseRC0IdentityDryRunFailsClosed(t *testing.T) {
	ctx := context.Background()
	old := schemaUpTo(t, "00006") // identity_epoch does not exist yet
	var out bytes.Buffer
	err := identitySwitchCommand(ctx, old, "production", []string{"--realm", "crane", "--dry-run"}, &out)
	if err == nil || strings.Contains(out.String(), `"dryRun":true`) {
		t.Fatal("dry-run on a schema without identity_epoch reported success:", out.String(), err)
	}
}

// Switching to the dedicated realm without one approved, linked administrator
// would lock everybody out: the switch must be refused.
func TestDatabaseRC0SwitchRefusedWithoutLinkedAdmin(t *testing.T) {
	ctx := context.Background()
	db := b2Database(t)
	shared := deployedProfiles["production"].Realms["crane"].Issuer
	dedicated := deployedProfiles["production"].Realms["baobei"].Issuer
	if _, _, _, err := switchIdentity(ctx, db, shared, "rc0-operator", "初始"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := identitySwitchCommand(ctx, db, "production", []string{"--realm", "baobei", "--operator", "rc0-operator", "--reason", "切换"}, &out)
	var current string
	_ = db.QueryRow(ctx, `SELECT issuer FROM identity_epoch`).Scan(&current)
	if err == nil || current != shared {
		t.Fatal("switch to a realm without a linked administrator was executed:", current, out.String(), err)
	}
	_ = dedicated
}
