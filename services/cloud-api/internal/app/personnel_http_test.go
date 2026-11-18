package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/personnel"
	"github.com/guolichen007/crane-operations-platform/internal/personnel/personneltest"
)

// personnelFixture: the full production handler chain (BFF session, CSRF, request id, audit) with
// a faithful in-memory Keycloak user store. The real-Keycloak path is proven separately
// (tools/provision-keycloak against Keycloak 26.8.1 and the B4 browser acceptance).
func personnelFixture(t *testing.T) (securityFixture, *personneltest.Fake) {
	t.Helper()
	f := newSecurityFixture(t)
	idp := personneltest.NewFake()
	f.api.personnel = &personnel.Service{DB: f.db, IdP: idp, Issuer: f.provider.Server.URL, RevokeLive: f.api.revokeLiveFor}
	// user-admin (project platform_admin on P001) becomes the system administrator; user-a stays a
	// plain viewer; user-maintainer becomes an account manager.
	for user, role := range map[string]string{"user-admin": "system_admin", "user-maintainer": "account_manager"} {
		if _, err := f.db.Exec(`INSERT INTO system_memberships(user_id,role,granted_by) VALUES($1,$2,'fixture')`, user, role); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-T','demo','PS-T','测试组合') ON CONFLICT DO NOTHING`,
		`INSERT INTO departments(id,name) VALUES('ops','运维部')`,
	} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return f, idp
}

func (f securityFixture) jsonCall(t *testing.T, cookie *http.Cookie, csrf, method, path string, headers map[string]string, body any) (int, map[string]any) {
	t.Helper()
	w := f.doBody(cookie, csrf, method, path, headers, body)
	out := map[string]any{}
	if w.Body.Len() > 0 && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w.Code, out
}

func TestPersonnelAPIAnonymousAndUnauthorizedAreRefused(t *testing.T) {
	f, _ := personnelFixture(t)
	routes := [][2]string{{"GET", "/api/v1/admin/people"}, {"POST", "/api/v1/admin/people"}, {"GET", "/api/v1/admin/people/user-a"},
		{"PATCH", "/api/v1/admin/people/user-a"}, {"POST", "/api/v1/admin/people/user-a/disable"}, {"POST", "/api/v1/admin/people/user-a/enable"},
		{"POST", "/api/v1/admin/people/user-a/reset-password"}, {"POST", "/api/v1/admin/people/user-a/revoke-sessions"},
		{"PUT", "/api/v1/admin/people/user-a/system-roles"}, {"PUT", "/api/v1/admin/people/user-a/portfolio-roles/PS-T"},
		{"GET", "/api/v1/admin/departments"}, {"POST", "/api/v1/admin/departments"}, {"GET", "/api/v1/admin/provisions"},
		{"GET", "/api/v1/admin/portfolio-scopes"}}
	for _, r := range routes {
		if w := f.request(nil, "", r[1], r[0]); w.Code != 401 {
			t.Fatalf("anonymous %s %s = %d", r[0], r[1], w.Code)
		}
	}
	// A project platform_admin / portfolio role holder without a system role gets 403 everywhere
	// (and never 404-vs-403 differences that leak existence for writes).
	viewer, vs := f.login(t, "a")
	if _, err := f.db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('user-a','PS-T','portfolio_manager')`); err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		code, _ := f.jsonCall(t, viewer, vs.CsrfToken, r[0], r[1], map[string]string{"If-Match": `"1"`, "Idempotency-Key": "key-viewer-0001"}, map[string]any{})
		if code != 403 && code != 400 && code != 428 {
			t.Fatalf("viewer %s %s = %d", r[0], r[1], code)
		}
		if code != 403 && r[0] == "GET" {
			t.Fatalf("viewer read %s = %d", r[1], code)
		}
	}
	// CSRF: a system admin's write without the token is refused before the handler runs.
	admin, _ := f.login(t, "admin")
	if code, _ := f.jsonCall(t, admin, "", "POST", "/api/v1/admin/people/user-a/revoke-sessions", nil, nil); code != 403 {
		t.Fatal("missing CSRF accepted", code)
	}
	// /admin/me answers for everybody with their own roles only.
	code, me := f.jsonCall(t, viewer, vs.CsrfToken, "GET", "/api/v1/admin/me", nil, nil)
	if code != 200 || len(me["systemRoles"].([]any)) != 0 {
		t.Fatal("me", code, me)
	}
	// System admin has no project business access it did not already have (P002 stays 404).
	if w := f.request(admin, "", "/api/v1/devices/SIM-P002-01", "GET"); w.Code != 404 {
		t.Fatal("system role leaked project access", w.Code)
	}
}

func TestPersonnelAPIProvisionDisableResetAndLiveRevocation(t *testing.T) {
	f, idp := personnelFixture(t)
	admin, as := f.login(t, "admin")
	body := map[string]any{"username": "new-hire", "profile": map[string]any{"displayName": "新员工", "departmentId": "ops", "jobTitle": "操作工", "phone": "", "email": ""},
		"systemRoles": []string{}, "portfolioGrants": []map[string]string{}}
	code, out := f.jsonCall(t, admin, as.CsrfToken, "POST", "/api/v1/admin/people", map[string]string{"Idempotency-Key": "key-http-0001"}, body)
	if code != 201 || out["state"] != "COMPLETED" || len(out["temporaryPassword"].(string)) != 16 {
		t.Fatal("provision", code, out)
	}
	code, again := f.jsonCall(t, admin, as.CsrfToken, "POST", "/api/v1/admin/people", map[string]string{"Idempotency-Key": "key-http-0001"}, body)
	if code != 200 || again["temporaryPassword"] != nil || again["replayed"] != true {
		t.Fatal("replay", code, again)
	}
	if code, _ = f.jsonCall(t, admin, as.CsrfToken, "POST", "/api/v1/admin/people", map[string]string{"Idempotency-Key": "key-http-0002"}, body); code != 409 {
		t.Fatal("duplicate username", code)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='personnel_created' AND target_user_id='new-hire' AND request_id<>''`); n != 1 {
		t.Fatal("audit", n)
	}

	// Disable an ALREADY LOGGED-IN operator: existing cookie and WebSocket stop at once.
	op, os := f.login(t, "operator")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(f.server.URL, "https")+"/api/v1/devices/SIM-P001-01/stream",
		&websocket.DialOptions{HTTPClient: f.server.Client(), HTTPHeader: http.Header{"Cookie": []string{op.String()}, "Origin": []string{f.server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if _, _, err = conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if code, _ = f.jsonCall(t, op, os.CsrfToken, "POST", "/api/v1/devices/SIM-P001-01/live-sessions", nil, nil); code != 201 {
		t.Fatal("live lease", code)
	}
	var version int64
	_ = f.db.QueryRow(`SELECT version FROM users WHERE id='user-operator'`).Scan(&version)
	code, res := f.jsonCall(t, admin, as.CsrfToken, "POST", "/api/v1/admin/people/user-operator/disable", map[string]string{"If-Match": `"` + itoa64(version) + `"`}, nil)
	if code != 200 || res["sessionsRevoked"].(float64) < 1 || res["identitySync"] != "UNMANAGED" {
		t.Fatal("disable", code, res)
	}
	if w := f.request(op, "", "/api/v1/devices", "GET"); w.Code != 401 {
		t.Fatal("disabled user's cookie still works", w.Code)
	}
	if n := f.count(t, `SELECT count(*) FROM live_sessions WHERE user_id='user-operator'`); n != 0 {
		t.Fatal("live lease survived")
	}
	for {
		if _, _, err = conn.Read(ctx); err != nil {
			break
		}
	}
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatal("WebSocket survived disable", err)
	}

	// Reset password of the managed new hire: returns once, old sessions gone.
	kc, _ := idp.FindByUsername(ctx, "new-hire")
	idp.Sessions[kc.ID] = 2
	code, rp := f.jsonCall(t, admin, as.CsrfToken, "POST", "/api/v1/admin/people/new-hire/reset-password", nil, nil)
	if code != 200 || rp["temporaryPassword"] == out["temporaryPassword"] || idp.Sessions[kc.ID] != 0 {
		t.Fatal("reset", code, rp)
	}
	// Last admin protection over HTTP.
	if code, res = f.jsonCall(t, admin, as.CsrfToken, "PUT", "/api/v1/admin/people/user-admin/system-roles", nil, map[string]any{"roles": []string{}, "expectedRoles": []string{"system_admin"}}); code != 422 {
		t.Fatal("self demotion", code, res)
	}
	mgr, ms := f.login(t, "maintainer")
	if code, _ = f.jsonCall(t, mgr, ms.CsrfToken, "POST", "/api/v1/admin/people/user-admin/disable", map[string]string{"If-Match": `"1"`}, nil); code != 403 {
		t.Fatal("account manager disabled the administrator", code)
	}
	if code, _ = f.jsonCall(t, mgr, ms.CsrfToken, "PUT", "/api/v1/admin/people/new-hire/system-roles", nil, map[string]any{"roles": []string{"system_admin"}, "expectedRoles": []string{}}); code != 403 {
		t.Fatal("account manager escalated", code)
	}
	// Re-enable: revoked project grant is not restored; never-revoked grant stays effective.
	if err = identity.ChangeGrant(context.Background(), f.db, "user-operator", "P001", "", true, auditlog.Event{Actor: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if code, _ = f.jsonCall(t, admin, as.CsrfToken, "POST", "/api/v1/admin/people/user-operator/enable", map[string]string{"If-Match": `"` + itoa64(version+1) + `"`}, nil); code != 200 {
		t.Fatal("enable", code)
	}
	if n := f.count(t, `SELECT count(*) FROM project_memberships WHERE user_id='user-operator'`); n != 0 {
		t.Fatal("revoked grant restored")
	}
}

func TestProjectMemberPickerIsScopedAndMinimal(t *testing.T) {
	f, _ := personnelFixture(t)
	if _, err := f.db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('user-maintainer','PS-T','portfolio_manager'),('user-a','PS-T','portfolio_planner')`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO projects(id,tenant_id,name,portfolio_scope_id,project_phase) VALUES('PRJ-T','demo','测试项目','PS-T','REGISTERED')`,
		`INSERT INTO project_intakes(id,portfolio_scope_id,tenant_id,intake_code,project_name,project_type,status,formal_project_id) VALUES('IN-T','PS-T','demo','IN-T','测试项目','NEW','APPROVED','PRJ-T')`,
	} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	mgr, ms := f.login(t, "maintainer")
	w := f.request(mgr, ms.CsrfToken, "/api/v1/portfolio/projects/PRJ-T/member-candidates?q=Anonymous", "GET")
	if w.Code != 200 {
		t.Fatal("manager picker", w.Code, w.Body.String())
	}
	var list []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) == 0 {
		t.Fatal("no candidates")
	}
	for _, c := range list {
		for k := range c {
			if k != "userId" && k != "displayName" && k != "username" && k != "departmentName" && k != "jobTitle" {
				t.Fatal("picker leaks field", k)
			}
		}
	}
	if w = f.request(mgr, ms.CsrfToken, "/api/v1/portfolio/projects/PRJ-T/member-candidates?q=", "GET"); w.Code != 422 {
		t.Fatal("empty query must not enumerate", w.Code)
	}
	planner, ps := f.login(t, "a")
	if w = f.request(planner, ps.CsrfToken, "/api/v1/portfolio/projects/PRJ-T/member-candidates?q=Anonymous", "GET"); w.Code != 403 {
		t.Fatal("planner may not search people", w.Code)
	}
	op, os := f.login(t, "operator")
	if w = f.request(op, os.CsrfToken, "/api/v1/portfolio/projects/PRJ-T/member-candidates?q=Anonymous", "GET"); w.Code != 404 {
		t.Fatal("outsider must not see the project", w.Code)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
