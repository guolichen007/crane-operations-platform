package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func identityFixture() []byte {
	return []byte(`{"approvalReference":"SYNTHETIC-APPROVAL-001","teams":[{"id":"66666666-6666-4666-8666-666666666666","name":"Synthetic Identity Team"}],"users":[{"subject":"77777777-7777-4777-8777-777777777777","displayName":"Synthetic Identity","role":"baobei_sales","team":"66666666-6666-4666-8666-666666666666","active":true}]}`)
}
func TestIdentityInput(t *testing.T) {
	if _, e := parseIdentity(identityFixture()); e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct{ name, old, new string }{
		{"unknown_role", "baobei_sales", "realm_admin"},
		{"subject_not_uuid", "77777777-7777-4777-8777-777777777777", "synthetic-name"},
		{"missing_active", `,"active":true`, ``},
		{"sales_no_team", `"team":"66666666-6666-4666-8666-666666666666"`, `"team":""`},
		{"admin_with_team", "baobei_sales", "baobei_admin"},
		{"null_users", `"users":[`, `"users":null,"unknown":[`},
		{"duplicate_key", `"active":true`, `"active":true,"active":false`},
		{"unknown_field", `"active":true`, `"active":true,"password":"synthetic"`},
		{"approval_required", `"approvalReference":"SYNTHETIC-APPROVAL-001",`, ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, e := parseIdentity([]byte(strings.Replace(string(identityFixture()), test.old, test.new, 1))); e == nil {
				t.Fatal("accepted invalid identity")
			}
		})
	}
	for _, teamField := range []string{``, `,"team":null`} {
		b := []byte(`{"approvalReference":"SYNTHETIC-APPROVAL","teams":[],"users":[{"subject":"77777777-7777-4777-8777-777777777777","displayName":"Synthetic","role":"baobei_admin","active":true` + teamField + `}]}`)
		if _, e := parseIdentity(b); e == nil {
			t.Fatal("admin team must be explicit empty string")
		}
	}
	if _, e := parseIdentity([]byte{0xff}); e == nil {
		t.Fatal("invalid UTF-8")
	}
	var m IdentityManifest
	_ = json.Unmarshal(identityFixture(), &m)
	m.Users = append(m.Users, m.Users[0])
	b, _ := json.Marshal(m)
	if _, e := parseIdentity(b); e == nil {
		t.Fatal("duplicate subject accepted")
	}
	if _, e := parseIdentity(append(identityFixture(), []byte(` {}`)...)); e == nil {
		t.Fatal("trailing data")
	}
	if _, e := parseIdentity([]byte(strings.Repeat(" ", identityLimit+1))); e == nil {
		t.Fatal("oversized file")
	}
}
func TestDatabaseIdentityApply(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	b := identityFixture()
	count := func(table string) int {
		t.Helper()
		var n int
		if e := db.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	initialUsers := count("app_users")
	initialTeams := count("teams")
	initialAudit := count("audit_events")
	sum, e := applyIdentity(ctx, db, b, "synthetic-operator", true)
	if e != nil || sum.UsersChanged != 1 || sum.TeamsChanged != 1 {
		t.Fatal(sum, e)
	}
	if count("app_users") != initialUsers || count("teams") != initialTeams || count("audit_events") != initialAudit {
		t.Fatal("dry-run committed")
	}
	sum, e = applyIdentity(ctx, db, b, "synthetic-operator", false)
	if e != nil || sum.UsersChanged != 1 || sum.TeamsChanged != 1 {
		t.Fatal(sum, e)
	}
	var team, role, name string
	var active bool
	if e = db.QueryRow(ctx, `SELECT team_id::text,role,display_name,active FROM app_users WHERE subject='77777777-7777-4777-8777-777777777777'`).Scan(&team, &role, &name, &active); e != nil || team != "66666666-6666-4666-8666-666666666666" || role != "baobei_sales" || !active || name != "Synthetic Identity" {
		t.Fatal("identity relations", e)
	}
	if count("app_users") != initialUsers+1 || count("audit_events") != initialAudit+1 {
		t.Fatal("omitted user lost or missing audit")
	}
	var details map[string]any
	var raw []byte
	if e = db.QueryRow(ctx, `SELECT details FROM audit_events WHERE action='IDENTITY_APPLY'`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal(raw, &details)
	if details["operator"] != "synthetic-operator" || details["approval_reference"] != "SYNTHETIC-APPROVAL-001" || details["manifest_sha256"] != sum.ManifestSHA256 || strings.Contains(string(raw), "Synthetic Identity") {
		t.Fatal("audit evidence")
	}
	sum, e = applyIdentity(ctx, db, b, "synthetic-operator", false)
	if e != nil || sum.UsersChanged+sum.TeamsChanged != 0 || count("audit_events") != initialAudit+1 {
		t.Fatal("not idempotent", sum, e)
	}
	// Two concurrent repeats cannot create duplicate audit or users.
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := applyIdentity(ctx, db, b, "synthetic-operator", false)
			if e != nil || s.UsersChanged != 0 {
				t.Error(s, e)
			}
		}()
	}
	wg.Wait()
	// Validation failure after a team upsert rolls back the entire transaction.
	bad := strings.Replace(string(b), `"team":"66666666-6666-4666-8666-666666666666"`, `"team":"99999999-9999-4999-8999-999999999999"`, 1)
	bad = strings.Replace(bad, "Synthetic Identity Team", "Uncommitted Team", 1)
	if _, e = applyIdentity(ctx, db, []byte(bad), "synthetic-operator", false); e == nil {
		t.Fatal("unknown team accepted")
	}
	if e = db.QueryRow(ctx, `SELECT name FROM teams WHERE id=$1`, team).Scan(&name); e != nil || name != "Synthetic Identity Team" {
		t.Fatal("partial transaction", e)
	}
	if _, e = applyIdentity(ctx, db, b, "", false); e == nil {
		t.Fatal("missing operator")
	}
	// A live session is revoked on explicit deactivation; a dry run does not revoke it.
	a := &App{db: db, cfg: Config{Env: "development"}}
	request := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	if e = a.session(rec, request, "77777777-7777-4777-8777-777777777777", "baobei_sales"); e != nil {
		t.Fatal(e)
	}
	request.AddCookie(rec.Result().Cookies()[0])
	if _, e = a.user(request); e != nil {
		t.Fatal(e)
	}
	inactive := []byte(strings.Replace(string(b), `"active":true`, `"active":false`, 1))
	if s, e := applyIdentity(ctx, db, inactive, "synthetic-operator", true); e != nil || s.SessionsRevoked != 1 {
		t.Fatal(s, e)
	}
	if _, e = a.user(request); e != nil {
		t.Fatal("dry-run revoked session")
	}
	if s, e := applyIdentity(ctx, db, inactive, "synthetic-operator", false); e != nil || s.SessionsRevoked != 1 {
		t.Fatal(s, e)
	}
	if _, e = a.user(request); e == nil {
		t.Fatal("deactivated session survived")
	}
	if e = a.session(httptest.NewRecorder(), request, "77777777-7777-4777-8777-777777777777", "baobei_sales"); e == nil {
		t.Fatal("inactive session created")
	}
	// Changing roles also requires a new matching client token; stale roles cannot create sessions.
	elevated := []byte(strings.Replace(strings.Replace(string(b), "baobei_sales", "baobei_admin", 1), `"team":"66666666-6666-4666-8666-666666666666"`, `"team":""`, 1))
	if _, e = applyIdentity(ctx, db, elevated, "synthetic-operator", false); e != nil {
		t.Fatal(e)
	}
	if e = a.session(httptest.NewRecorder(), request, "77777777-7777-4777-8777-777777777777", "baobei_sales"); e == nil {
		t.Fatal("stale client role accepted")
	}
}
