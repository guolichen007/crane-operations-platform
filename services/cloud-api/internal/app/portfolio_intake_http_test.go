package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPortfolioIntakeRegistrationFlowHTTP is the backend half of the Q1 project
// registration acceptance: the exact calls the PortfolioWorkspace form makes, through
// the real BFF session + CSRF + portfolio authorization on an isolated PostgreSQL.
func TestPortfolioIntakeRegistrationFlowHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	portfolioSecurityData(t, f)
	pm, sessionPM := f.login(t, "pm")          // portfolio_manager on PS-A, no ProjectGrant
	a, sessionA := f.login(t, "a")             // portfolio_planner on PS-A (+ viewer on P001)
	b, sessionB := f.login(t, "b")             // portfolio_planner on PS-B only
	admin, sessionAdmin := f.login(t, "admin") // platform_admin ProjectGrant, no portfolio membership

	body := `{"portfolioScopeId":"PS-A","intakeCode":"Q1-001","projectName":"Q1 登记验收项目","customerName":null,"projectType":"NEW_INSTALLATION","priority":"HIGH","riskLevel":"MEDIUM","scopeSummary":"浏览器登记","siteReference":null,"requestedStart":null,"requestedHandover":null,"plannedStart":"2026-12-01T00:00:00Z","plannedEnd":"2027-03-01T00:00:00Z","projectManagerUserId":null,"technicalLeadUserId":null,"siteLeadUserId":null}`

	// No portfolio membership in the scope → 403, nothing written (platform_admin is not a bypass).
	if w := f.requestJSON(b, sessionB.CsrfToken, "/api/v1/portfolio/intakes", "POST", body); w.Code != 403 {
		t.Fatalf("PS-B planner created a PS-A intake: %d %s", w.Code, w.Body.String())
	}
	if w := f.requestJSON(admin, sessionAdmin.CsrfToken, "/api/v1/portfolio/intakes", "POST", body); w.Code != 403 {
		t.Fatalf("platform_admin without portfolio membership created an intake: %d", w.Code)
	}

	// Contract violations are 422 with the reason, never 503 and never a partial row.
	for name, bad := range map[string]string{
		"blank name":       strings.Replace(body, `"projectName":"Q1 登记验收项目"`, `"projectName":"   "`, 1),
		"bad priority":     strings.Replace(body, `"priority":"HIGH"`, `"priority":"URGENT"`, 1),
		"end before start": strings.Replace(body, `"plannedEnd":"2027-03-01T00:00:00Z"`, `"plannedEnd":"2026-11-01T00:00:00Z"`, 1),
		"missing field":    strings.Replace(body, `"siteLeadUserId":null`, `"x":null`, 1),
	} {
		if w := f.requestJSON(pm, sessionPM.CsrfToken, "/api/v1/portfolio/intakes", "POST", bad); w.Code != 422 {
			t.Fatalf("%s: want 422, got %d %s", name, w.Code, w.Body.String())
		}
	}
	var rows int
	if err := f.db.QueryRow(`SELECT count(*) FROM project_intakes WHERE portfolio_scope_id='PS-A'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rejected input wrote rows: %d %v", rows, err)
	}

	// The planner creates the intake (201) and sees it after a reload.
	w := f.requestJSON(a, sessionA.CsrfToken, "/api/v1/portfolio/intakes", "POST", body)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct{ Id, Status string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Status != "REGISTERED" || created.Id == "" {
		t.Fatalf("created intake: %s", w.Body.String())
	}
	if w := f.request(a, sessionA.CsrfToken, "/api/v1/portfolio/intakes", "GET"); !strings.Contains(w.Body.String(), created.Id) {
		t.Fatalf("created intake missing from list: %s", w.Body.String())
	}
	// Same code in the same scope → 409 (not 503).
	if w := f.requestJSON(pm, sessionPM.CsrfToken, "/api/v1/portfolio/intakes", "POST", body); w.Code != 409 {
		t.Fatalf("duplicate code: want 409, got %d %s", w.Code, w.Body.String())
	}

	path := "/api/v1/portfolio/intakes/" + created.Id
	// Planner advances REGISTERED → REVIEW → PLANNING but cannot approve (403); skipping states is 409.
	if w := f.requestJSON(a, sessionA.CsrfToken, path+"/transitions", "POST", `{"status":"APPROVED","reason":"skip"}`); w.Code != 409 {
		t.Fatalf("skip to APPROVED: %d", w.Code)
	}
	for _, to := range []string{"REVIEW", "PLANNING"} {
		if w := f.requestJSON(a, sessionA.CsrfToken, path+"/transitions", "POST", `{"status":"`+to+`","reason":"运营推进"}`); w.Code != 200 {
			t.Fatalf("transition %s: %d %s", to, w.Code, w.Body.String())
		}
	}
	if w := f.requestJSON(a, sessionA.CsrfToken, path+"/approve", "POST", `{}`); w.Code != 403 {
		t.Fatalf("planner approved: %d", w.Code)
	}
	// Manager approves → formal project id.
	w = f.requestJSON(pm, sessionPM.CsrfToken, path+"/approve", "POST", `{}`)
	if w.Code != 200 {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	var approved struct{ ProjectId string }
	_ = json.Unmarshal(w.Body.Bytes(), &approved)
	if approved.ProjectId == "" {
		t.Fatalf("approve returned no project id: %s", w.Body.String())
	}
	if w := f.requestJSON(pm, sessionPM.CsrfToken, path+"/approve", "POST", `{}`); w.Code != 409 {
		t.Fatalf("second approve: %d", w.Code)
	}
	if w := f.request(pm, sessionPM.CsrfToken, path, "GET"); !strings.Contains(w.Body.String(), `"formalProjectId":"`+approved.ProjectId+`"`) || !strings.Contains(w.Body.String(), `"status":"APPROVED"`) {
		t.Fatalf("intake does not show the formal project: %s", w.Body.String())
	}

	// Approval does not grant project access and does not create an Inventory/map project:
	// the formal project has lifecycle NULL (not in /api/v1/projects) and no memberships.
	var grants int
	var lifecycle *string
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM project_memberships WHERE project_id=$1), (SELECT lifecycle FROM projects WHERE id=$1)`, approved.ProjectId).Scan(&grants, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if grants != 0 || lifecycle != nil {
		t.Fatalf("approval auto-granted (%d) or entered Inventory (lifecycle=%v)", grants, lifecycle)
	}
	if w := f.request(pm, sessionPM.CsrfToken, "/api/v1/projects", "GET"); strings.Contains(w.Body.String(), approved.ProjectId) {
		t.Fatalf("approving manager sees the formal project in Inventory/map: %s", w.Body.String())
	}
	if w := f.request(admin, sessionAdmin.CsrfToken, "/api/v1/projects", "GET"); strings.Contains(w.Body.String(), approved.ProjectId) {
		t.Fatalf("platform_admin sees the ungranted formal project: %s", w.Body.String())
	}
	if w := f.request(pm, sessionPM.CsrfToken, "/api/v1/assets?project="+approved.ProjectId, "GET"); strings.Contains(w.Body.String(), approved.ProjectId) {
		t.Fatalf("manager without grant sees assets of the formal project: %d %s", w.Code, w.Body.String())
	}
	if w := f.requestJSON(pm, sessionPM.CsrfToken, "/api/v1/assets", "POST", `{"projectId":"`+approved.ProjectId+`","assetType":"CRANE","assetCode":"Q1-CRN","displayName":"x"}`); w.Code == 201 {
		t.Fatalf("manager without grant created an asset in the formal project")
	}
}
