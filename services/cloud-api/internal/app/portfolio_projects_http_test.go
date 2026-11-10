package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

// q2Fixture: PS-A (user-pm manager, user-a planner), PS-B (user-pmb manager), one approved
// formal project in each scope (FP-A, FP-B) plus a legacy project, and grant targets.
func q2Fixture(t *testing.T) securityFixture {
	t.Helper()
	f := newSecurityFixture(t)
	portfolioSecurityData(t, f)
	ctx := context.Background()
	for _, u := range []struct{ id, sub string }{{"user-pmb", "subject-pmb"}, {"user-new", "subject-new"}, {"user-other", "subject-other"}} {
		if err := identity.Bind(ctx, f.db, u.id, f.provider.Server.URL, u.sub, "Anonymous "+u.id, true, auditlog.Event{Actor: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('user-pmb','PS-B','portfolio_manager')`,
		`INSERT INTO projects(id,tenant_id,name,portfolio_scope_id,project_phase) VALUES('FP-A','demo','正式项目 A','PS-A','REGISTERED'),('FP-B','demo','正式项目 B','PS-B','REGISTERED'),('FP-UNAPPROVED','demo','未批准','PS-A','REGISTERED')`,
		`INSERT INTO project_intakes(id,portfolio_scope_id,tenant_id,intake_code,project_name,project_type,status,formal_project_id) VALUES('I-A','PS-A','demo','I-A','正式项目 A','NEW','APPROVED','FP-A'),('I-B','PS-B','demo','I-B','正式项目 B','NEW','APPROVED','FP-B'),('I-U','PS-A','demo','I-U','未批准','NEW','PLANNING',NULL)`,
		// A disabled user and a user of a foreign identity provider: never grantable.
		`INSERT INTO users(id,subject,issuer,enabled,display_name) VALUES('user-disabled','s-dis','` + f.provider.Server.URL + `',false,'disabled'),('user-foreign','s-for','https://elsewhere.example/realms/x',true,'foreign'),('user-local','s-loc',NULL,true,'local')`,
	} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatalf("%v: %s", err, stmt)
		}
	}
	return f
}

func grantBody(user string, role, expected *string) string {
	j := func(p *string) string {
		if p == nil {
			return "null"
		}
		return `"` + *p + `"`
	}
	return fmt.Sprintf(`{"userId":%q,"role":%s,"expectedRole":%s}`, user, j(role), j(expected))
}
func rp(v string) *string { return &v }

func (f securityFixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// snapshot of everything a grant change may touch: memberships + successful grant audits.
func (f securityFixture) grantState(t *testing.T) string {
	t.Helper()
	rows, err := f.db.Query(`SELECT user_id||':'||project_id||':'||role FROM project_memberships ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		out = append(out, v)
	}
	return strings.Join(out, ",") + fmt.Sprintf("|audits=%d", f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_role_changed'`))
}

func TestPortfolioFormalProjectsVisibilityHTTP(t *testing.T) {
	f := q2Fixture(t)
	pm, spm := f.login(t, "pm")
	a, sa := f.login(t, "a")
	admin, sadmin := f.login(t, "admin")
	pmb, spmb := f.login(t, "pmb")

	// Manager of PS-A: sees FP-A only (not FP-B, not the unapproved project, not Inventory P001).
	w := f.request(pm, spm.CsrfToken, "/api/v1/portfolio/projects", "GET")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"FP-A"`) || strings.Contains(w.Body.String(), "FP-B") || strings.Contains(w.Body.String(), "FP-UNAPPROVED") || strings.Contains(w.Body.String(), `"P001"`) {
		t.Fatalf("manager list: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"grantStatus":"NO_MEMBERS"`) || !strings.Contains(w.Body.String(), `"canManageGrants":true`) || !strings.Contains(w.Body.String(), `"inventoryRegistered":false`) {
		t.Fatalf("manager list fields: %s", w.Body.String())
	}
	// Planner of PS-A: sees FP-A, cannot manage, cannot read members (403, membership known).
	w = f.request(a, sa.CsrfToken, "/api/v1/portfolio/projects", "GET")
	if !strings.Contains(w.Body.String(), `"canManageGrants":false`) || strings.Contains(w.Body.String(), "FP-B") {
		t.Fatalf("planner list: %s", w.Body.String())
	}
	if w := f.request(a, sa.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", "GET"); w.Code != 403 {
		t.Fatalf("planner members: %d", w.Code)
	}
	// platform_admin with no portfolio membership: empty list, foreign ids hidden (404).
	if w := f.request(admin, sadmin.CsrfToken, "/api/v1/portfolio/projects", "GET"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("platform_admin list: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/portfolio/projects/FP-A", "/api/v1/portfolio/projects/FP-A/members"} {
		if w := f.request(admin, sadmin.CsrfToken, path, "GET"); w.Code != 404 {
			t.Fatalf("platform_admin %s: %d", path, w.Code)
		}
	}
	// Guessing ids: other scope / unapproved / Inventory / nonexistent are all 404 for PS-A's manager.
	for _, id := range []string{"FP-B", "FP-UNAPPROVED", "P001", "does-not-exist", "bad%20id"} {
		if w := f.request(pm, spm.CsrfToken, "/api/v1/portfolio/projects/"+id, "GET"); w.Code != 404 {
			t.Fatalf("guessed %s: %d", id, w.Code)
		}
	}
	if w := f.request(pmb, spmb.CsrfToken, "/api/v1/portfolio/projects/FP-B", "GET"); w.Code != 200 || !strings.Contains(w.Body.String(), "正式项目 B") {
		t.Fatalf("PS-B manager detail: %d %s", w.Code, w.Body.String())
	}
	// Database unavailable → 503 (never an empty list).
	f.db.Exec(`ALTER TABLE project_intakes RENAME TO project_intakes_gone`)
	if w := f.request(pm, spm.CsrfToken, "/api/v1/portfolio/projects", "GET"); w.Code != 503 {
		t.Fatalf("db failure: %d %s", w.Code, w.Body.String())
	}
	f.db.Exec(`ALTER TABLE project_intakes_gone RENAME TO project_intakes`)
}

func TestPortfolioProjectGrantLifecycleHTTP(t *testing.T) {
	f := q2Fixture(t)
	pm, spm := f.login(t, "pm")
	newbie, snew := f.login(t, "new")
	path := "/api/v1/portfolio/projects/FP-A/members"

	// Before any grant: the user has no access to FP-A (no auto-grant on approval).
	if w := f.request(newbie, snew.CsrfToken, "/api/v1/assets?project=FP-A", "GET"); strings.Contains(w.Body.String(), "FP-A") {
		t.Fatalf("ungranted user sees FP-A: %s", w.Body.String())
	}
	// (5) first grant: platform_admin on FP-A → session lists it, asset create works.
	w := f.requestJSON(pm, spm.CsrfToken, path, "POST", grantBody("user-new", rp("platform_admin"), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"userId":"user-new"`) {
		t.Fatalf("first grant: %d %s", w.Code, w.Body.String())
	}
	if w := f.request(newbie, snew.CsrfToken, "/auth/session", "GET"); !strings.Contains(w.Body.String(), `"projectId":"FP-A"`) {
		t.Fatalf("granted user's session lacks FP-A: %s", w.Body.String())
	}
	w = f.requestJSON(newbie, snew.CsrfToken, "/api/v1/assets", "POST", `{"projectId":"FP-A","assetType":"CRANE","assetCode":"Q2-CRN-1","displayName":"Q2 行车"}`)
	if w.Code != 201 {
		t.Fatalf("granted platform_admin cannot create asset in FP-A: %d %s", w.Code, w.Body.String())
	}
	if w := f.request(newbie, snew.CsrfToken, "/api/v1/assets?project=FP-A", "GET"); !strings.Contains(w.Body.String(), "Q2-CRN-1") {
		t.Fatalf("granted user cannot list own asset: %s", w.Body.String())
	}
	// The manager (portfolio rights only) still cannot read or write FP-A assets (9).
	if w := f.request(pm, spm.CsrfToken, "/api/v1/assets?project=FP-A", "GET"); strings.Contains(w.Body.String(), "Q2-CRN-1") {
		t.Fatalf("portfolio manager reads project assets: %s", w.Body.String())
	}
	if w := f.requestJSON(pm, spm.CsrfToken, "/api/v1/assets", "POST", `{"projectId":"FP-A","assetType":"CRANE","assetCode":"Q2-PM"}`); w.Code == 201 {
		t.Fatal("portfolio manager wrote a project asset")
	}
	// Role change platform_admin → viewer: read stays, write is gone immediately.
	if w := f.requestJSON(pm, spm.CsrfToken, path, "POST", grantBody("user-new", rp("viewer"), rp("platform_admin"))); w.Code != 200 {
		t.Fatalf("downgrade: %d %s", w.Code, w.Body.String())
	}
	if w := f.requestJSON(newbie, snew.CsrfToken, "/api/v1/assets", "POST", `{"projectId":"FP-A","assetType":"CRANE","assetCode":"Q2-CRN-2"}`); w.Code != 403 {
		t.Fatalf("viewer still writes after downgrade: %d", w.Code)
	}
	// (6) revoke: the SAME (old) session immediately loses access.
	if w := f.requestJSON(pm, spm.CsrfToken, path, "POST", grantBody("user-new", nil, rp("viewer"))); w.Code != 200 || strings.Contains(w.Body.String(), "user-new") {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if w := f.request(newbie, snew.CsrfToken, "/api/v1/assets?project=FP-A", "GET"); strings.Contains(w.Body.String(), "Q2-CRN-1") {
		t.Fatalf("old session still reads FP-A after revoke: %s", w.Body.String())
	}
	var assetID string
	_ = f.db.QueryRow(`SELECT id FROM asset_devices WHERE asset_code='Q2-CRN-1'`).Scan(&assetID)
	if w := f.request(newbie, snew.CsrfToken, "/api/v1/assets/"+assetID, "GET"); w.Code != 404 {
		t.Fatalf("old session direct asset read after revoke: %d", w.Code)
	}
	if w := f.request(newbie, snew.CsrfToken, "/auth/session", "GET"); strings.Contains(w.Body.String(), "FP-A") {
		t.Fatalf("session still lists FP-A after revoke: %s", w.Body.String())
	}
	// (8) exactly one audit per successful change, with actor / project / request id.
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_role_changed' AND project_id='FP-A' AND user_id='user-pm' AND request_id<>''`); n != 3 {
		t.Fatalf("grant audits = %d, want 3", n)
	}
	for _, want := range []string{"granted:user-new:platform_admin", "granted:user-new:viewer", "revoked:user-new"} {
		if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_role_changed' AND result=$1`, want); n != 1 {
			t.Fatalf("audit %s = %d", want, n)
		}
	}
	// intake_created audit (D): created in the same transaction as the row.
	pmBody := `{"portfolioScopeId":"PS-A","intakeCode":"Q2-AUD","projectName":"审计","customerName":null,"projectType":"T","priority":"LOW","riskLevel":"LOW","scopeSummary":"","siteReference":null,"requestedStart":null,"requestedHandover":null,"plannedStart":null,"plannedEnd":null,"projectManagerUserId":null,"technicalLeadUserId":null,"siteLeadUserId":null}`
	w = f.requestJSON(pm, spm.CsrfToken, "/api/v1/portfolio/intakes", "POST", pmBody)
	var created struct{ Id string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if w.Code != 201 || f.count(t, `SELECT count(*) FROM audit_logs WHERE action='intake_created' AND user_id='user-pm' AND result=$1 AND request_id<>''`, "success:"+created.Id+":PS-A") != 1 {
		t.Fatalf("intake_created audit missing: %d %s", w.Code, w.Body.String())
	}
}

func TestPortfolioProjectGrantRefusalsHTTP(t *testing.T) {
	f := q2Fixture(t)
	pm, spm := f.login(t, "pm")
	a, sa := f.login(t, "a")
	admin, sadmin := f.login(t, "admin")
	pmb, spmb := f.login(t, "pmb")
	before := f.grantState(t)
	type attempt struct {
		name       string
		cookie     *http.Cookie
		csrf, path string
		body       string
		want       int
	}
	for _, c := range []attempt{
		// (1)/(7) a manager cannot touch another scope's project; foreign ids are 404.
		{"PS-B manager on FP-A", pmb, spmb.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-new", rp("viewer"), nil), 404},
		{"PS-A manager on FP-B", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-B/members", grantBody("user-new", rp("viewer"), nil), 404},
		// (2) platform_admin without the portfolio scope.
		{"platform_admin on FP-A", admin, sadmin.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-new", rp("viewer"), nil), 404},
		// (3) planner holds the scope but not project_grant_manage.
		{"planner", a, sa.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-new", rp("viewer"), nil), 403},
		// Not a formal project / not approved / Inventory / unknown.
		{"unapproved project", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-UNAPPROVED/members", grantBody("user-new", rp("viewer"), nil), 404},
		{"inventory project", pm, spm.CsrfToken, "/api/v1/portfolio/projects/P001/members", grantBody("user-new", rp("viewer"), nil), 404},
		// (4) invalid user / role / body.
		{"unknown user", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-nobody", rp("viewer"), nil), 422},
		{"disabled user", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-disabled", rp("viewer"), nil), 422},
		{"foreign issuer", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-foreign", rp("viewer"), nil), 422},
		{"no issuer", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-local", rp("viewer"), nil), 422},
		{"self grant", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-pm", rp("platform_admin"), nil), 422},
		{"bad role", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-new", rp("portfolio_manager"), nil), 422},
		{"super role", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-new", rp("admin"), nil), 422},
		{"bad user id", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user new", rp("viewer"), nil), 422},
		{"project in body ignored/unknown field", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", `{"userId":"user-new","role":"viewer","expectedRole":null,"projectId":"FP-B"}`, 422},
		{"revoke nothing", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-new", nil, nil), 409},
		{"stale expected role", pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", grantBody("user-new", rp("viewer"), rp("operator")), 409},
		{"no csrf", pm, "", "/api/v1/portfolio/projects/FP-A/members", grantBody("user-new", rp("viewer"), nil), 403},
	} {
		if w := f.requestJSON(c.cookie, c.csrf, c.path, "POST", c.body); w.Code != c.want {
			t.Errorf("%s: want %d, got %d %s", c.name, c.want, w.Code, w.Body.String())
		}
	}
	if after := f.grantState(t); after != before {
		t.Fatalf("refused requests changed grants/audit:\nbefore %s\nafter  %s", before, after)
	}
	// Denials of scope are audited as authorization_denied (no silent refusal).
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='authorization_denied' AND result LIKE 'portfolio%'`); n < 4 {
		t.Fatalf("denials not audited: %d", n)
	}
	// (7) a successful grant on FP-A does not touch FP-B or other users.
	if w := f.requestJSON(pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", "POST", grantBody("user-new", rp("operator"), nil)); w.Code != 200 {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	if after := f.grantState(t); !strings.Contains(after, "user-new:FP-A:operator") {
		t.Fatalf("unexpected state %s", after)
	}
	if f.count(t, `SELECT count(*) FROM project_memberships WHERE project_id='FP-B' OR (user_id<>'user-new' AND project_id='FP-A')`) != 0 {
		t.Fatal("grant leaked to other project/users")
	}
	// Revoking the manager's portfolio membership takes effect on the next request (old session).
	f.db.Exec(`DELETE FROM portfolio_memberships WHERE user_id='user-pm'`)
	if w := f.requestJSON(pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", "POST", grantBody("user-new", rp("viewer"), rp("operator"))); w.Code != 404 {
		t.Fatalf("revoked manager still manages: %d", w.Code)
	}
}

// (10) concurrency / duplicates / out-of-order: with the same expectedRole only one of N
// concurrent changes wins (exactly one success, one audit); the rest are 409 with no write.
func TestPortfolioProjectGrantConcurrencyHTTP(t *testing.T) {
	f := q2Fixture(t)
	pm, spm := f.login(t, "pm")
	path := "/api/v1/portfolio/projects/FP-A/members"
	roles := []string{"viewer", "operator", "maintainer", "platform_admin", "viewer", "operator"}
	codes := make([]int, len(roles))
	var wg sync.WaitGroup
	for i, role := range roles {
		wg.Add(1)
		go func(i int, role string) {
			defer wg.Done()
			codes[i] = f.requestJSON(pm, spm.CsrfToken, path, "POST", grantBody("user-new", rp(role), nil)).Code
		}(i, role)
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
			conflict++
		default:
			t.Fatalf("unexpected status %d in %v", c, codes)
		}
	}
	if ok != 1 || conflict != len(roles)-1 {
		t.Fatalf("concurrent first grants: %v", codes)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_role_changed' AND project_id='FP-A'`); n != 1 {
		t.Fatalf("audits after race = %d", n)
	}
	var current string
	_ = f.db.QueryRow(`SELECT role FROM project_memberships WHERE user_id='user-new' AND project_id='FP-A'`).Scan(&current)
	// Exact duplicate resubmission of the winner → 409 (already in that state), no audit.
	if w := f.requestJSON(pm, spm.CsrfToken, path, "POST", grantBody("user-new", rp(current), nil)); w.Code != 409 {
		t.Fatalf("duplicate resubmission: %d", w.Code)
	}
	// Out-of-order: a revoke issued after reading "current" succeeds; a late role change that
	// still carries the old expectedRole arrives afterwards and is refused (does not resurrect).
	if w := f.requestJSON(pm, spm.CsrfToken, path, "POST", grantBody("user-new", nil, rp(current))); w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if w := f.requestJSON(pm, spm.CsrfToken, path, "POST", grantBody("user-new", rp("platform_admin"), rp(current))); w.Code != 409 {
		t.Fatalf("stale request resurrected the grant: %d", w.Code)
	}
	if f.count(t, `SELECT count(*) FROM project_memberships WHERE user_id='user-new'`) != 0 {
		t.Fatal("grant resurrected")
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_role_changed' AND project_id='FP-A'`); n != 2 {
		t.Fatalf("audits = %d, want 2 (grant + revoke)", n)
	}
}
