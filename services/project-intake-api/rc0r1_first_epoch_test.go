package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// G5-RC0-R1: production starts at 00005 (no identity_epoch). After 00006–00008
// the table exists but is empty. The Runbook must work from exactly there,
// without any fixture pre-seeding the epoch.
func TestDatabaseRC0R1FirstEpochFrom00005(t *testing.T) {
	ctx := context.Background()
	db := schemaUpTo(t, "00006") // real 00001–00005 schema + one legacy member
	run := func(args ...string) (map[string]any, error) {
		var out bytes.Buffer
		err := identitySwitchCommand(ctx, db, "production", args, &out)
		var m map[string]any
		_ = json.Unmarshal(out.Bytes(), &m)
		return m, err
	}
	// Legacy business data with a shared-realm administrator (sub = subject).
	admin := newUUID()
	var adminID string
	if err := db.QueryRow(ctx, `INSERT INTO app_users(subject,display_name,role,active,staff_status,staff_managed,work_phone)
	 VALUES($1,'现网管理员','baobei_admin',true,'ACTIVE',true,'+8613990000001') RETURNING staff_id::text`, admin).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	// Before the upgrade: any precheck refuses (schema not ready).
	if _, err := run("--realm", "crane", "--dry-run"); !errors.Is(err, errSwitchSchema) {
		t.Fatal("precheck on 00005 must refuse", err)
	}
	// SCHEMA_UPGRADE: 00006, 00007, 00008.
	if err := migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM identity_epoch`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("identity_epoch must exist and be empty after the upgrade", rows, err)
	}
	// The new API refuses to start on an empty epoch.
	if err := verifyIdentityEpoch(ctx, db, "production", deployedProfiles["production"].Realms["crane"].Issuer); !errors.Is(err, errIdentityEpochMismatch) {
		t.Fatal("API started on an empty epoch", err)
	}
	// Directly into the dedicated realm from the empty epoch: refused (dry-run and real).
	if _, err := run("--realm", "baobei", "--takeover-admin", adminID, "--dry-run"); !errors.Is(err, errSwitchEpoch) {
		t.Fatal("dry-run into baobei from an empty epoch", err)
	}
	if _, err := run("--realm", "baobei", "--operator", "rc0r1-op", "--reason", "直接切入", "--takeover-admin", adminID, "--approval", "CHG-1"); !errors.Is(err, errSwitchEpoch) {
		t.Fatal("switch into baobei from an empty epoch", err)
	}
	// IDENTITY_EPOCH_INIT: explicitly establish the shared crane epoch.
	pre, err := run("--realm", "crane", "--dry-run")
	if err != nil || pre["precheck"] != "PASS" {
		t.Fatal("crane init dry-run", pre, err)
	}
	if r := pre["report"].(map[string]any); r["epoch"].(float64) != 0 || r["from"] != "" {
		t.Fatal("dry-run must report the empty starting point", r)
	}
	init, err := run("--realm", "crane", "--operator", "rc0r1-op", "--reason", "维护窗口内建立共享身份纪元")
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	var epoch int
	if err = db.QueryRow(ctx, `SELECT issuer,epoch FROM identity_epoch`).Scan(&issuer, &epoch); err != nil ||
		issuer != deployedProfiles["production"].Realms["crane"].Issuer || epoch != 1 || init["epoch"].(float64) != 1 {
		t.Fatal("initial epoch", issuer, epoch, init, err)
	}
	if err = verifyIdentityEpoch(ctx, db, "production", issuer); err != nil {
		t.Fatal("crane API after init", err)
	}
	// Re-running the init is a normal switch (epoch 2), never a second "first".
	// Not needed here; the next step is the dedicated realm.
	// Before links are imported: baobei refused for lack of a linked admin.
	if _, err = run("--realm", "baobei", "--takeover-admin", adminID, "--dry-run"); !errors.Is(err, errSwitchAdmin) {
		t.Fatal("baobei without links", err)
	}
	// Identity links imported (identity-migration.py links output, executed in the window).
	dedicated := deployedProfiles["production"].Realms["baobei"].Issuer
	if _, err = db.Exec(ctx, `INSERT INTO identity_links(issuer,external_subject,subject) VALUES($1,$2,$3)`, dedicated, newUUID(), admin); err != nil {
		t.Fatal(err)
	}
	pre, err = run("--realm", "baobei", "--takeover-admin", adminID, "--dry-run")
	if err != nil || pre["precheck"] != "PASS" {
		t.Fatal("baobei precheck after links", pre, err)
	}
	// A session from the crane epoch, revoked by the switch.
	if _, err = db.Exec(ctx, `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at,issuer,identity_epoch)
	 SELECT 'rc0r1-crane',$1,'c',now()+interval '1 hour',issuer,epoch FROM identity_epoch`, admin); err != nil {
		t.Fatal(err)
	}
	sw, err := run("--realm", "baobei", "--operator", "rc0r1-op", "--reason", "切换", "--takeover-admin", adminID, "--approval", "CHG-2")
	if err != nil || sw["revokedSessions"].(float64) != 1 {
		t.Fatal("switch to baobei", sw, err)
	}
	if err = verifyIdentityEpoch(ctx, db, "production", dedicated); err != nil {
		t.Fatal("baobei API after switch", err)
	}
	if err = verifyIdentityEpoch(ctx, db, "production", issuer); err == nil {
		t.Fatal("crane API must now refuse")
	}
	// Switch back to crane: sessions revoked again, the legacy admin can sign in
	// with sub = subject again; anything opened only in baobei is reported.
	if _, err = db.Exec(ctx, `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at,issuer,identity_epoch)
	 SELECT 'rc0r1-baobei',$1,'c',now()+interval '1 hour',issuer,epoch FROM identity_epoch`, admin); err != nil {
		t.Fatal(err)
	}
	back, err := run("--realm", "crane", "--operator", "rc0r1-op", "--reason", "回切", "--takeover-admin", adminID, "--approval", "CHG-3")
	if err != nil || back["revokedSessions"].(float64) != 1 || back["epoch"].(float64) != 3 {
		t.Fatal("switch back", back, err)
	}
	var left int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM app_sessions`).Scan(&left)
	if left != 0 {
		t.Fatal("sessions survived", left)
	}
	var audit string
	_ = db.QueryRow(ctx, `SELECT string_agg(details->>'to', ' > ' ORDER BY revision) FROM audit_events WHERE action='IDENTITY_SWITCH'`).Scan(&audit)
	if strings.Count(audit, ">") != 2 || !strings.HasPrefix(audit, issuer) {
		t.Fatal("switch history", audit)
	}
}
