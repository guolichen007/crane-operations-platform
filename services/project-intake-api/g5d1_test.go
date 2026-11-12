package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// G5-D1: draft reason validation, team archive vs. invitation concurrency and
// legacy routes, against real PostgreSQL 18.4.

func TestDatabaseG5D1DraftReason(t *testing.T) {
	f := newAccountFixture(t)
	sales := login(t, f.server, "synthetic-a")
	s, b := sales.call("POST", "/api/v1/projects", sample("合成原因校验"))
	p := projectFrom(t, s, b)
	field := func(b []byte) (string, string) {
		var e struct {
			Code    string
			Details struct{ Field string }
		}
		_ = json.Unmarshal(b, &e)
		return e.Code, e.Details.Field
	}
	long := strings.Repeat("长", 1001)
	for _, c := range []struct {
		name, reason, code string
	}{{"missing", "", "REASON_REQUIRED"}, {"spaces", "   \t\n ", "REASON_REQUIRED"}, {"too long", long, "REASON_TOO_LONG"}} {
		body := map[string]any{"version": p.Version}
		if c.name != "missing" {
			body["reason"] = c.reason
		}
		s, b = sales.call("POST", "/api/v1/projects/"+p.ID+"/discard", body)
		if code, fld := field(b); s != 400 || code != c.code || fld != "reason" {
			t.Fatalf("discard %s: %d %s", c.name, s, b)
		}
	}
	// Nothing changed by the refused requests.
	s, b = sales.call("GET", "/api/v1/projects/"+p.ID, nil)
	if got := projectFrom(t, s, b); got.IntakeStatus != "DRAFT" || got.Version != p.Version {
		t.Fatal("refused discard changed the draft", got.IntakeStatus, got.Version)
	}
	// Exactly 1000 characters (multi-byte) is accepted and stored trimmed.
	ok := "  " + strings.Repeat("因", 1000) + "  "
	s, b = sales.call("POST", "/api/v1/projects/"+p.ID+"/discard", map[string]any{"version": p.Version, "reason": ok})
	d := projectFrom(t, s, b)
	var stored string
	_ = f.app.db.QueryRow(context.Background(), `SELECT details->>'reason' FROM audit_events WHERE project_id=$1 AND action='project.discard'`, p.ID).Scan(&stored)
	if utf8.RuneCountInString(stored) != 1000 || strings.HasPrefix(stored, " ") {
		t.Fatal("stored reason not trimmed", utf8.RuneCountInString(stored))
	}
	// Restore keeps the published contract: reason optional; blank accepted as
	// "no reason"; over-long refused with the same field error.
	s, b = sales.call("POST", "/api/v1/projects/"+p.ID+"/restore", map[string]any{"version": d.Version, "reason": long})
	if code, fld := field(b); s != 400 || code != "REASON_TOO_LONG" || fld != "reason" {
		t.Fatalf("restore too long: %d %s", s, b)
	}
	s, b = sales.call("POST", "/api/v1/projects/"+p.ID+"/restore", map[string]any{"version": d.Version, "reason": "   "})
	r := projectFrom(t, s, b)
	if r.IntakeStatus != "DRAFT" {
		t.Fatal("restore without reason", r.IntakeStatus)
	}
}

// The staff write path holds the exclusive identity lock for the whole request,
// so an invitation into a team and the archive of that team are serialized:
// whichever commits second sees the first.
func TestDatabaseG5D1TeamArchiveConcurrency(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	newTeam := func(name string) staffTeam {
		s, b := f.admin.call("POST", "/api/v1/teams", map[string]any{"name": name, "reason": "并发"})
		var tm staffTeam
		_ = json.Unmarshal(b, &tm)
		expect(t, s, 200, b)
		return tm
	}
	invite := func(team, phone string) (int, []byte) {
		return f.admin.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "合成并发成员", Phone: phone, Role: salesRole, TeamID: team, Reason: "并发"})
	}
	archive := func(tm staffTeam) (int, []byte) {
		return f.admin.call("POST", "/api/v1/teams/"+tm.ID+"/archive", map[string]any{"version": tm.Version, "reason": "并发"})
	}
	// Deterministic order 1: an invitation is in flight (holds the lock and has
	// inserted the member) while the archive request arrives.
	tm := newTeam("合成并发团队一")
	tx, err := f.app.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO app_users(staff_id,subject,display_name,role,team_id,active,staff_status,staff_managed)
	 SELECT v,'pending:'||v::text,'合成在途邀请','baobei_sales',$1,false,'PENDING_PROVISION',true FROM (SELECT gen_random_uuid() v) x`, tm.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan [2]any, 1)
	go func() { s, b := archive(tm); done <- [2]any{s, b} }()
	select {
	case <-done:
		t.Fatal("archive did not wait for the in-flight invitation")
	case <-time.After(400 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res[0].(int) != 409 || !strings.Contains(string(res[1].([]byte)), "TEAM_HAS_MEMBERS") {
		t.Fatal("archive after a committed invitation", res[0], string(res[1].([]byte)))
	}
	// Deterministic order 2: the archive is in flight; the invitation waits and
	// then sees the archived team.
	tm2 := newTeam("合成并发团队二")
	tx, _ = f.app.db.Begin(ctx)
	_, _ = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock)
	if _, err = tx.Exec(ctx, `UPDATE teams SET status='ARCHIVED',archived_at=now(),staff_version=staff_version+1 WHERE id=$1`, tm2.ID); err != nil {
		t.Fatal(err)
	}
	go func() { s, b := invite(tm2.ID, "13970000001"); done <- [2]any{s, b} }()
	select {
	case <-done:
		t.Fatal("invitation did not wait for the in-flight archive")
	case <-time.After(400 * time.Millisecond):
	}
	_ = tx.Commit(ctx)
	res = <-done
	if res[0].(int) != 409 || !strings.Contains(string(res[1].([]byte)), "TEAM_ARCHIVED") {
		t.Fatal("invitation into an archived team", res[0], string(res[1].([]byte)))
	}
	// Free races through the real API: the invariant always holds.
	for round := 0; round < 15; round++ {
		tm := newTeam("合成并发团队轮" + string(rune('A'+round)))
		var wg sync.WaitGroup
		wg.Add(2)
		var si, sa int
		go func() {
			defer wg.Done()
			si, _ = invite(tm.ID, "1397100"+string(rune('0'+round/10))+string(rune('0'+round%10))+"00")
		}()
		go func() { defer wg.Done(); sa, _ = archive(tm) }()
		wg.Wait()
		if si == 200 && sa == 200 {
			t.Fatal("both an invitation and the archive succeeded", round)
		}
	}
	var bad int
	_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM app_users u JOIN teams t ON t.id=u.team_id
	 WHERE t.status='ARCHIVED' AND u.staff_status NOT IN ('ARCHIVED','REVOKED')`).Scan(&bad)
	if bad != 0 {
		t.Fatal("archived team has active members", bad)
	}
}

func TestDatabaseG5D1Routes(t *testing.T) {
	f := newAccountFixture(t)
	sales := login(t, f.server, "synthetic-a")
	mgr := login(t, f.server, "synthetic-manager")
	list := func(c testClient, path string) (int, int) {
		s, b := c.call("GET", path, nil)
		var r struct{ Total int }
		_ = json.Unmarshal(b, &r)
		return s, r.Total
	}
	for _, c := range []testClient{f.admin, mgr} {
		s1, n1 := list(c, "/api/v1/staff")
		s2, n2 := list(c, "/api/v1/staff/")
		if s1 != 200 || s2 != 200 || n1 != n2 {
			t.Fatal("trailing slash list", s1, s2, n1, n2)
		}
		s3, _ := list(c, "/api/v1/staff/?q=合成&page=1")
		if s3 != 200 {
			t.Fatal("trailing slash with query", s3)
		}
	}
	s, _ := sales.call("GET", "/api/v1/staff/", nil)
	if s != 403 {
		t.Fatal("sales must still be refused", s)
	}
	s, b := f.admin.call("GET", "/api/v1/teams/", nil)
	if s != 200 || !strings.Contains(string(b), `"items"`) {
		t.Fatal("teams trailing slash", s, string(b))
	}
	// Unknown members, malformed paths and other teams stay hidden as 404.
	for _, p := range []string{"/api/v1/staff/00000000-0000-4000-8000-000000000000", "/api/v1/staff//audit", "/api/v1/staff/not-a-uuid", "/api/v1/staff/invitations/", "/api/v1/staff/invitations"} {
		if s, b := f.admin.call("GET", p, nil); s != 404 {
			t.Fatal("must be 404", p, s, string(b))
		}
	}
	// A manager asking for a member of another team: hidden (404), not 200.
	var other string
	_ = f.app.db.QueryRow(context.Background(), `SELECT staff_id::text FROM app_users WHERE subject='synthetic-c'`).Scan(&other)
	if s, _ := mgr.call("GET", "/api/v1/staff/"+other, nil); s != 404 {
		t.Fatal("other team member must stay hidden", s)
	}
	// Field errors carry details.field; code/message unchanged for old clients.
	s, b = f.admin.call("POST", "/api/v1/teams", map[string]any{"name": "", "reason": "x"})
	if s != 400 || !strings.Contains(string(b), `"field":"name"`) || !strings.Contains(string(b), "STAFF_INVALID_INPUT") {
		t.Fatal("team name field error", s, string(b))
	}
	s, b = f.admin.call("POST", "/api/v1/teams", map[string]any{"name": "合成", "reason": " "})
	if s != 400 || !strings.Contains(string(b), `"field":"reason"`) {
		t.Fatal("team reason field error", s, string(b))
	}
	s, b = f.admin.call("POST", "/api/v1/staff/invitations", map[string]any{"name": "", "phone": "123", "role": salesRole, "teamId": syntheticTeam})
	if s != 400 || !strings.Contains(string(b), "STAFF_NAME_INVALID") || !strings.Contains(string(b), `"field":"name"`) {
		t.Fatal("staff name field error", s, string(b))
	}
}
