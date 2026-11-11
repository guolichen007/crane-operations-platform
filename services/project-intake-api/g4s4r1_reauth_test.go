package main

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// G4-S4-R1: needsApproval must never let the sales-to-sales shortcut override sensitive.
func TestG4S4R1NeedsApprovalMatrix(t *testing.T) {
	for _, failure := range needsApprovalMatrix(needsApproval) {
		t.Error(failure)
	}
}

func needsApprovalMatrix(fn func(User, string, string, *time.Time, bool) error) []string {
	var failures []string
	fresh, stale := time.Now(), time.Now().Add(-10*time.Minute)
	admin, manager := User{Role: adminRole}, User{Role: managerRole}
	for _, tc := range []struct {
		name             string
		actor            User
		oldRole, newRole string
		at               *time.Time
		sensitive        bool
		want             error
	}{
		{"sales identity re-verification, stale proof", admin, salesRole, salesRole, &stale, true, staffReauth},
		{"sales identity re-verification, no proof", admin, salesRole, salesRole, nil, true, staffReauth},
		{"sales identity re-verification, fresh proof", admin, salesRole, salesRole, &fresh, true, nil},
		{"sales identity re-verification by manager", manager, salesRole, salesRole, &fresh, true, staffDenied},
		{"ordinary sales edit by admin, stale", admin, salesRole, salesRole, &stale, false, nil},
		{"ordinary sales edit by manager, no proof", manager, salesRole, salesRole, nil, false, nil},
		{"appoint manager, stale", admin, salesRole, managerRole, &stale, true, staffReauth},
		{"appoint manager, fresh", admin, salesRole, managerRole, &fresh, true, nil},
		{"appoint manager by manager", manager, salesRole, managerRole, &fresh, true, staffDenied},
		{"ordinary manager edit, stale", admin, managerRole, managerRole, &stale, false, nil},
		{"manager edits a manager", manager, managerRole, managerRole, &fresh, false, staffDenied},
	} {
		if got := fn(tc.actor, tc.oldRole, tc.newRole, tc.at, tc.sensitive); got != tc.want {
			failures = append(failures, tc.name+": got "+errText(got)+", want "+errText(tc.want))
		}
	}
	return failures
}

func errText(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// Mutation check: the pre-R1 implementation (sales shortcut before sensitive) and a
// version with the reauth check deleted must both fail the matrix.
func TestG4S4R1MutationsAreDetected(t *testing.T) {
	preR1 := func(actor User, oldRole, newRole string, at *time.Time, sensitive bool) error {
		if oldRole == salesRole && newRole == salesRole {
			return nil
		}
		if actor.Role != adminRole {
			return staffDenied
		}
		if sensitive && !recentReauth(at) {
			return staffReauth
		}
		return nil
	}
	noReauth := func(actor User, oldRole, newRole string, at *time.Time, sensitive bool) error {
		if !(oldRole == salesRole && newRole == salesRole) && actor.Role != adminRole || sensitive && actor.Role != adminRole {
			return staffDenied
		}
		return nil
	}
	for name, mutant := range map[string]func(User, string, string, *time.Time, bool) error{"pre-R1 shortcut": preR1, "reauth check deleted": noReauth} {
		failures := needsApprovalMatrix(mutant)
		if len(failures) == 0 {
			t.Errorf("mutation %q not detected", name)
		} else {
			t.Logf("mutation %q detected: %s", name, strings.Join(failures, "; "))
		}
	}
	// Source-level guard: the sensitive branch must check the fresh proof.
	src, err := os.ReadFile("staff.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	body = body[strings.Index(body, "func needsApproval("):]
	body = body[:strings.Index(body, "\n}\n")]
	if !strings.Contains(body, "if sensitive {") || !strings.Contains(body, "!recentReauth(at)") {
		t.Fatal("needsApproval lost its sensitive reauthentication check")
	}
}

// End to end over HTTP: sales identity re-verification needs a fresh proof; ordinary
// sales edits and permissions do not; appointing a manager still does.
func TestDatabaseG4S4R1SalesReverification(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	app := &App{db: db, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	admin := login(t, server, "synthetic-admin")
	const subject = "aaaaaaaa-1111-4111-8111-111111111111"
	var id string
	var version int
	if err := db.QueryRow(ctx, `INSERT INTO app_users(subject,display_name,role,team_id,work_email,staff_managed)
 VALUES($1,'合成待核验销售','baobei_sales',$2,'reverify@example.test',true) RETURNING staff_id::text,staff_version`, subject, syntheticTeam).Scan(&id, &version); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/staff/" + id
	stale := func() {
		if _, err := db.Exec(ctx, `UPDATE staff_sessions SET reauthenticated_at=now()-interval '10 minutes'`); err != nil {
			t.Fatal(err)
		}
	}
	freshen := func() {
		if _, err := db.Exec(ctx, `UPDATE staff_sessions SET reauthenticated_at=now()`); err != nil {
			t.Fatal(err)
		}
	}
	profile := staffProfileInput{Name: "合成待核验销售", Email: "reverify@example.test", Role: salesRole, TeamID: syntheticTeam, Version: version}

	stale()
	// Ordinary sales edit: no reauthentication.
	edit := profile
	edit.Note = "日常备注"
	s, b := admin.call("PATCH", path, edit)
	m := memberFrom(t, s, b)
	// Sales business permissions: no reauthentication.
	s, b = admin.call("PATCH", path+"/permissions", staffMutation{Version: m.Version, Reason: "日常调整", Permissions: []string{"project.create"}})
	m = memberFrom(t, s, b)
	// Identity re-verification of a sales member: stale proof is refused.
	verify := profile
	verify.Note, verify.Version, verify.VerifyIdentity, verify.Reason = "日常备注", m.Version, true, "重新核验企业身份"
	s, b = admin.call("PATCH", path, verify)
	expect(t, s, 403, b)
	if !strings.Contains(string(b), "STAFF_REAUTH_REQUIRED") {
		t.Fatal(string(b))
	}
	var status string
	if err := db.QueryRow(ctx, `SELECT staff_status FROM app_users WHERE staff_id=$1`, id).Scan(&status); err != nil || status != staffActive {
		t.Fatal("refused re-verification must not change the member", status, err)
	}
	// A manager cannot start identity re-verification at all.
	if _, err := db.Exec(ctx, `UPDATE app_users SET team_id=$1 WHERE subject='synthetic-manager'`, syntheticTeam); err != nil {
		t.Fatal(err)
	}
	manager := login(t, server, "synthetic-manager")
	s, b = manager.call("PATCH", path, verify)
	expect(t, s, 403, b)
	// Fresh proof: accepted and queued for identity operations.
	freshen()
	s, b = admin.call("PATCH", path, verify)
	if m = memberFrom(t, s, b); m.Status != staffPending || m.SyncStatus != "PENDING" {
		t.Fatalf("re-verification not queued: %+v", m)
	}
	// Appointing a manager still needs the fresh proof.
	stale()
	lead := staffProfileInput{Name: "合成任命主管", Phone: "13940000001", Role: managerRole, TeamID: syntheticTeam, Reason: "任命"}
	s, b = admin.call("POST", "/api/v1/staff/invitations", lead)
	expect(t, s, 403, b)
}
