package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// V1.1 go-live (00016): first-time account claim.

func (f *accountFixture) inviteClaim(name, claimLogin, role string) staffMember {
	f.t.Helper()
	in := staffProfileInput{Name: name, Role: role, TeamID: syntheticTeam, ClaimLogin: claimLogin}
	if role == directorRole || role == adminRole {
		in.TeamID = ""
	}
	s, b := f.admin.call("POST", "/api/v1/staff/invitations", in)
	return memberFrom(f.t, s, b)
}

func (f *accountFixture) claim(c testClient, key, name, phone string) (int, claimResult, string) {
	f.t.Helper()
	raw, _ := json.Marshal(map[string]string{"name": name, "phone": phone})
	req, _ := http.NewRequest("POST", c.origin+"/api/v1/me/claim", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin)
	req.Header.Set("X-CSRF-Token", c.csrf)
	req.Header.Set("Idempotency-Key", key)
	resp, err := c.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out claimResult
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out, string(body)
}

// openClaimed: administrator issues a temporary name and opens the login; the
// member signs in with it (password already changed in Keycloak).
func (f *accountFixture) openClaimed(name, claimLogin, role string) (staffMember, testClient) {
	f.t.Helper()
	m := f.inviteClaim(name, claimLogin, role)
	s, res, b := f.account(f.admin, m.ID, "create", "create-"+claimLogin+"-00000001", map[string]string{})
	expect(f.t, s, 200, b)
	if res.Credential == nil || res.Credential.Login != claimLogin || res.Member.ClaimState != claimPending {
		f.t.Fatal("temporary credential", string(b))
	}
	c, s := f.signIn(claimLogin)
	expect(f.t, s, 200, nil)
	return m, c
}

func TestDatabaseGoLiveClaimClosedLoop(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	m, emp := f.openClaimed("销售01", "sales01", salesRole)

	// Until the claim, only GET /me and the claim itself are served.
	s, b := emp.call("GET", "/api/v1/me", nil)
	expect(t, s, 200, b)
	var me User
	_ = json.Unmarshal(b, &me)
	if !me.ClaimRequired {
		t.Fatal("claimRequired must be set", string(b))
	}
	for _, path := range []string{"/api/v1/projects", "/api/v1/me/profile", "/api/v1/followups", "/api/v1/customers", "/api/v1/analytics/overview", "/api/v1/reference-values"} {
		if s, b := emp.call("GET", path, nil); s != 403 || !strings.Contains(string(b), "ACCOUNT_CLAIM_REQUIRED") {
			t.Fatalf("%s served before the claim: %d %s", path, s, b)
		}
	}
	if s, b := emp.call("POST", "/api/v1/projects", map[string]any{}); s != 403 || !strings.Contains(string(b), "ACCOUNT_CLAIM_REQUIRED") {
		t.Fatalf("write served before the claim: %d %s", s, b)
	}
	// The administrator cannot fill in the phone for the member.
	s, b = f.admin.call("PATCH", "/api/v1/staff/"+m.ID, staffProfileInput{Name: "销售01", Phone: "13950001001", Role: salesRole, TeamID: syntheticTeam, Reason: "代填", Version: 2})
	if s != 409 {
		t.Fatalf("administrator set the phone of a pending claim: %d %s", s, b)
	}
	// Validation: name, phone format, duplicate phone.
	if s, _, b := f.claim(emp, "claim-key-000000000001", "", "13950001001"); s != 400 || !strings.Contains(b, "PROFILE_NAME_INVALID") {
		t.Fatal("empty name", s, b)
	}
	if s, _, b := f.claim(emp, "claim-key-000000000002", "王一", "12345"); s != 400 || !strings.Contains(b, "ACCOUNT_PHONE_MAINLAND_ONLY") {
		t.Fatal("bad phone", s, b)
	}
	other := f.invite("合成已有成员", "13950001999", salesRole)
	_ = other
	if s, _, b := f.claim(emp, "claim-key-000000000003", "王一", "13950001999"); s != 409 || !strings.Contains(b, "ACCOUNT_PHONE_TAKEN") {
		t.Fatal("duplicate phone", s, b)
	}
	// Unknown fields (role, team, subject) are refused.
	req, _ := http.NewRequest("POST", emp.origin+"/api/v1/me/claim", strings.NewReader(`{"name":"王一","phone":"13950001001","role":"baobei_admin"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", emp.origin)
	req.Header.Set("X-CSRF-Token", emp.csrf)
	req.Header.Set("Idempotency-Key", "claim-key-000000000004")
	resp, _ := emp.client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatal("role in claim accepted", resp.StatusCode)
	}

	var before struct {
		subject, team, role string
		link                int
	}
	_ = f.app.db.QueryRow(ctx, `SELECT subject,coalesce(team_id::text,''),role,(SELECT count(*) FROM identity_links l WHERE l.subject=u.subject) FROM app_users u WHERE staff_id=$1`, m.ID).
		Scan(&before.subject, &before.team, &before.role, &before.link)

	// The claim.
	s, res, body := f.claim(emp, "claim-key-000000000010", "王一", "139 5000 1001")
	if s != 200 || !res.Claimed || res.Login != "13950001001" {
		t.Fatal("claim", s, body)
	}
	// Same request again: replay, nothing done twice.
	if s, res, body := f.claim(emp, "claim-key-000000000010", "王一", "139 5000 1001"); s != 401 && !(s == 200 && res.Replayed) {
		t.Fatal("replay", s, body)
	}
	// The session ended; the temporary name no longer signs in, the phone does.
	if s, _ := emp.call("GET", "/api/v1/me", nil); s != 401 {
		t.Fatal("session survived the claim", s)
	}
	if _, s := f.signIn("sales01"); s == 200 {
		t.Fatal("temporary login name still signs in")
	}
	phoneClient, s := f.signIn("13950001001")
	expect(t, s, 200, nil)
	s, b = phoneClient.call("GET", "/api/v1/me", nil)
	_ = json.Unmarshal(b, &me)
	if s != 200 || me.ClaimRequired || !me.ProfileCompleted || me.Name != "王一" || me.Role != salesRole {
		t.Fatal("after claim", s, string(b))
	}
	if s, _ := phoneClient.call("GET", "/api/v1/projects", nil); s != 200 {
		t.Fatal("business API after claim", s)
	}
	// Identity, role and team unchanged; phone, claim state, audit stored.
	var after struct {
		subject, team, role, phone, state string
		login                             *string
		link, profile, audit, ops         int
	}
	_ = f.app.db.QueryRow(ctx, `SELECT subject,coalesce(team_id::text,''),role,work_phone,claim_state,claim_login,
 (SELECT count(*) FROM identity_links l WHERE l.subject=u.subject),
 (SELECT count(*) FROM profile_audit p WHERE p.staff_id=u.staff_id AND p.action='profile.confirm' AND p.new_name='王一'),
 (SELECT count(*) FROM staff_audit a WHERE a.target_id=u.staff_id AND a.action='staff.account.claim'),
 (SELECT count(*) FROM account_operations o WHERE o.staff_id=u.staff_id AND o.operation='CLAIM' AND o.state='DONE')
 FROM app_users u WHERE staff_id=$1`, m.ID).
		Scan(&after.subject, &after.team, &after.role, &after.phone, &after.state, &after.login, &after.link, &after.profile, &after.audit, &after.ops)
	if after.subject != before.subject || after.team != before.team || after.role != before.role || after.link != before.link || after.link != 1 ||
		after.phone != "+8613950001001" || after.state != claimDone || after.login != nil || after.profile != 1 || after.audit != 1 || after.ops != 1 {
		t.Fatalf("after claim: %+v (before %+v)", after, before)
	}
	// A completed member cannot claim again; the administrator manages the account normally.
	if s, _, b := f.claim(phoneClient, "claim-key-000000000011", "王一", "13950001002"); s != 409 || !strings.Contains(b, "ACCOUNT_CLAIM_NOT_PENDING") {
		t.Fatal("second claim", s, b)
	}
	if s, res, b := f.account(f.admin, m.ID, "reset-password", "reset-claimed-00000001", map[string]string{"reason": "忘记密码"}); s != 200 || res.Credential == nil || res.Credential.Login != "13950001001" {
		t.Fatal("reset after claim", s, string(b))
	}
}

func TestDatabaseGoLiveClaimConcurrentPhone(t *testing.T) {
	f := newAccountFixture(t)
	_, a := f.openClaimed("销售02", "sales02", salesRole)
	_, b := f.openClaimed("销售03", "sales03", salesRole)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, c := range []testClient{a, b} {
		wg.Add(1)
		go func(i int, c testClient) {
			defer wg.Done()
			codes[i], _, _ = f.claim(c, "claim-race-00000000"+string(rune('a'+i))+"x", "并发"+string(rune('甲'+i)), "13950002001")
		}(i, c)
	}
	wg.Wait()
	ok := 0
	for _, s := range codes {
		if s == 200 {
			ok++
		} else if s != 409 {
			t.Fatal("unexpected status", codes)
		}
	}
	if ok != 1 || f.dev.enabledFor("13950002001") != 1 {
		t.Fatal("exactly one member may claim a phone", codes, f.dev.enabledFor("13950002001"))
	}
}

func TestDatabaseGoLiveClaimInterruptedAndRecovered(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	m, emp := f.openClaimed("销售04", "sales04", salesRole)
	// The process dies right after Keycloak renamed the user.
	f.app.fault = func(point string) error {
		if point == "crash:after-claim-username" {
			return crashError{point}
		}
		return nil
	}
	s, _, _ := f.claim(emp, "claim-crash-000000001", "赵四", "13950003001")
	if s == 200 {
		t.Fatal("crashed claim reported success")
	}
	f.app.fault = nil
	// Claim still pending in the database; Keycloak already uses the phone.
	var state string
	_ = f.app.db.QueryRow(ctx, `SELECT claim_state FROM app_users WHERE staff_id=$1`, m.ID).Scan(&state)
	if state != claimPending {
		t.Fatal("claim state", state)
	}
	// Recovery after the lease expired finishes the claim from Keycloak's state.
	if _, err := f.app.db.Exec(ctx, `UPDATE account_operations SET lease_expires_at=now()-interval '1 second' WHERE staff_id=$1 AND state='STARTED'`, m.ID); err != nil {
		t.Fatal(err)
	}
	n, err := f.app.recoverAccountOps(ctx)
	if err != nil || n != 1 {
		t.Fatal("recovery", n, err)
	}
	var phone, name string
	_ = f.app.db.QueryRow(ctx, `SELECT claim_state,coalesce(work_phone,''),display_name FROM app_users WHERE staff_id=$1`, m.ID).Scan(&state, &phone, &name)
	if state != claimDone || phone != "+8613950003001" || name != "赵四" {
		t.Fatal("recovered claim", state, phone, name)
	}
	if _, s := f.signIn("13950003001"); s != 200 {
		t.Fatal("phone login after recovery", s)
	}
	// A claim whose rename never happened is settled as not applied; the member retries.
	m2, emp2 := f.openClaimed("销售05", "sales05", salesRole)
	f.app.fault = func(point string) error {
		if point == "db:step:KC_USERNAME" {
			return nil
		}
		if point == "crash:after-claim-username" {
			return crashError{point}
		}
		return nil
	}
	f.dev.failNext = "update"
	s, _, b := f.claim(emp2, "claim-fail-0000000001", "钱五", "13950003002")
	f.app.fault = nil
	if s == 200 {
		t.Fatal("failed rename reported success", b)
	}
	_ = f.app.db.QueryRow(ctx, `SELECT claim_state FROM app_users WHERE staff_id=$1`, m2.ID).Scan(&state)
	if state != claimPending {
		t.Fatal("claim state after failed rename", state)
	}
	emp2, s = f.signIn("sales05")
	expect(t, s, 200, nil)
	if s, _, b := f.claim(emp2, "claim-fail-0000000002", "钱五", "13950003002"); s != 200 {
		t.Fatal("retry after failure", s, b)
	}
}

func TestDatabaseGoLiveClaimRules(t *testing.T) {
	f := newAccountFixture(t)
	// Only the administrator issues temporary names; never together with a phone; unique.
	manager := login(t, f.server, "synthetic-manager")
	s, b := manager.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "主管代发", Role: salesRole, TeamID: syntheticTeam, ClaimLogin: "sales09"})
	if s != 403 {
		t.Fatal("manager issued a temporary name", s, string(b))
	}
	s, b = f.admin.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "带手机", Phone: "13950004001", Role: salesRole, TeamID: syntheticTeam, ClaimLogin: "sales10"})
	if s != 400 || !strings.Contains(string(b), "STAFF_CLAIM_LOGIN_WITH_PHONE") {
		t.Fatal("temporary name with phone", s, string(b))
	}
	for _, bad := range []string{"13950004002", "1abc", "ab", "Sales 11", "销售11"} {
		s, b = f.admin.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "坏名", Role: salesRole, TeamID: syntheticTeam, ClaimLogin: bad})
		if s != 400 {
			t.Fatal("bad temporary name accepted", bad, s, string(b))
		}
	}
	f.inviteClaim("唯一", "sales11", salesRole)
	s, b = f.admin.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "重复", Role: salesRole, TeamID: syntheticTeam, ClaimLogin: "SALES11"})
	if s != 409 || !strings.Contains(string(b), "STAFF_CLAIM_LOGIN_DUPLICATE") {
		t.Fatal("duplicate temporary name", s, string(b))
	}
	// An ordinary member (phone login) has nothing to claim.
	m := f.invite("普通成员", "13950004003", salesRole)
	s, _, raw := f.account(f.admin, m.ID, "create", "create-plain-000000001", map[string]string{})
	expect(t, s, 200, raw)
	c, s := f.signIn("13950004003")
	expect(t, s, 200, nil)
	if s, _, b := f.claim(c, "claim-plain-000000001", "普通成员", "13950004004"); s != 409 || !strings.Contains(b, "ACCOUNT_CLAIM_NOT_PENDING") {
		t.Fatal("claim without pending", s, b)
	}
	// Changing the phone of a pending claim through the account service is refused.
	p := f.inviteClaim("待认领", "sales12", salesRole)
	s, _, raw = f.account(f.admin, p.ID, "create", "create-sales12-0000001", map[string]string{})
	expect(t, s, 200, raw)
	s, _, raw = f.account(f.admin, p.ID, "change-phone", "phone-sales12-0000001", map[string]string{"reason": "代填", "phone": "13950004005"})
	if s != 409 || !strings.Contains(string(raw), "ACCOUNT_CLAIM_PENDING") {
		t.Fatal("change-phone during pending claim", s, string(raw))
	}
	// Reset of a pending claim returns the temporary name as the login.
	s, res, raw := f.account(f.admin, p.ID, "reset-password", "reset-sales12-0000001", map[string]string{"reason": "忘记密码"})
	if s != 200 || res.Credential == nil || res.Credential.Login != "sales12" {
		t.Fatal("reset of pending claim", s, string(raw))
	}
}
