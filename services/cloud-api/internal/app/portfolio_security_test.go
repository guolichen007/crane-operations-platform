package app

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

// portfolioSecurityData seeds two portfolio scopes (PS-A, PS-B) with resources,
// schedule items and one conflict each, plus memberships: user-a -> PS-A planner,
// user-b -> PS-B planner, and a new user-pm -> PS-A manager with NO project grant.
func portfolioSecurityData(t *testing.T, f securityFixture) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-A','demo','PS-A','Portfolio A'),('PS-B','demo','PS-B','Portfolio B')`,
		`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('user-a','PS-A','portfolio_planner'),('user-b','PS-B','portfolio_planner')`,
		`INSERT INTO resources(id,portfolio_scope_id,resource_type,name,capacity,exclusive,override_allowed,home_timezone,status,version) VALUES('R-A','PS-A','RUNWAY','Runway A',1,true,false,'UTC','ACTIVE',1),('R-B','PS-B','RUNWAY','Runway B',1,true,false,'UTC','ACTIVE',1)`,
		`INSERT INTO schedule_items(id,portfolio_scope_id,source_type,source_id,name,start_at,end_at,timezone,status,priority,version) VALUES('SI-A','PS-A','PROJECT_ACTIVITY','sa','Item A',now(),now()+interval '1 hour','UTC','PLANNED','MEDIUM',1),('SI-B','PS-B','PROJECT_ACTIVITY','sb','Item B',now(),now()+interval '1 hour','UTC','PLANNED','MEDIUM',1)`,
		`INSERT INTO schedule_conflicts(conflict_type,severity,left_item_id,right_item_id,resource_id,fingerprint,details_json,status) VALUES('RESOURCE_OVERLAP','BLOCKING','SI-A',NULL,'R-A','fp-a','{}','OPEN'),('RESOURCE_OVERLAP','BLOCKING','SI-B',NULL,'R-B','fp-b','{}','OPEN')`,
	} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	// A portfolio manager with NO project grant.
	if err := identity.Bind(ctx, f.db, "user-pm", f.provider.Server.URL, "subject-pm", "Portfolio Manager", true, auditlog.Event{Actor: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('user-pm','PS-A','portfolio_manager')`); err != nil {
		t.Fatal(err)
	}
}

func TestPortfolioHTTPListScopeFilter(t *testing.T) {
	f := newSecurityFixture(t)
	portfolioSecurityData(t, f)
	a, sessionA := f.login(t, "a")

	check := func(path string) {
		t.Helper()
		w := f.request(a, sessionA.CsrfToken, path, "GET")
		if w.Code != 200 {
			t.Fatalf("%s -> %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "PS-B") {
			t.Fatalf("%s leaked PS-B: %s", path, w.Body.String())
		}
	}
	for _, path := range []string{
		"/api/v1/portfolio/scopes",
		"/api/v1/portfolio/resources",
		"/api/v1/portfolio/schedule/items",
		"/api/v1/portfolio/conflicts",
	} {
		check(path)
	}
	// PS-A objects must be visible to the PS-A planner.
	if w := f.request(a, sessionA.CsrfToken, "/api/v1/portfolio/scopes", "GET"); !strings.Contains(w.Body.String(), "PS-A") {
		t.Fatalf("PS-A scope missing from own list: %s", w.Body.String())
	}
	if w := f.request(a, sessionA.CsrfToken, "/api/v1/portfolio/resources", "GET"); !strings.Contains(w.Body.String(), "R-A") {
		t.Fatalf("PS-A resource missing from own list: %s", w.Body.String())
	}
	// Direct object access to a foreign scope is hidden with 404.
	if w := f.request(a, sessionA.CsrfToken, "/api/v1/portfolio/resources/R-B", "GET"); w.Code != 404 {
		t.Fatalf("foreign resource not hidden: %d", w.Code)
	}
	if w := f.request(a, sessionA.CsrfToken, "/api/v1/portfolio/schedule/items/SI-B", "GET"); w.Code != 404 {
		t.Fatalf("foreign schedule item not hidden: %d", w.Code)
	}
	if w := f.request(a, sessionA.CsrfToken, "/api/v1/portfolio/intakes/PS-B-intake", "GET"); w.Code != 404 {
		t.Fatalf("foreign intake not hidden: %d", w.Code)
	}
}

func TestPortfolioPlatformAdminProjectGrantHasNoPortfolioBypass(t *testing.T) {
	f := newSecurityFixture(t)
	portfolioSecurityData(t, f)
	admin, sessionAdmin := f.login(t, "admin")
	// platform_admin with only a ProjectGrant and no portfolio membership sees
	// empty portfolio lists, never the whole platform.
	for _, path := range []string{
		"/api/v1/portfolio/scopes",
		"/api/v1/portfolio/resources",
		"/api/v1/portfolio/schedule/items",
		"/api/v1/portfolio/conflicts",
	} {
		w := f.request(admin, sessionAdmin.CsrfToken, path, "GET")
		if w.Code != 200 {
			t.Fatalf("%s -> %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "PS-A") || strings.Contains(w.Body.String(), "PS-B") {
			t.Fatalf("%s leaked portfolio objects to a project-only platform_admin: %s", path, w.Body.String())
		}
	}
	// Direct portfolio objects are hidden (404), not just forbidden.
	if w := f.request(admin, sessionAdmin.CsrfToken, "/api/v1/portfolio/resources/R-A", "GET"); w.Code != 404 {
		t.Fatalf("platform_admin without membership leaked resource: %d", w.Code)
	}
}

func TestPortfolioManagerWithoutProjectGrantCannotReadProjectAssets(t *testing.T) {
	f := newSecurityFixture(t)
	portfolioSecurityData(t, f)
	pm, sessionPM := f.login(t, "pm")
	// The portfolio membership must not leak project asset/device objects.
	if w := f.request(pm, sessionPM.CsrfToken, "/api/v1/assets/devices", "GET"); w.Code != 200 || strings.Contains(w.Body.String(), "ASSET-P001-01") {
		t.Fatalf("portfolio-only user read project assets: %d %s", w.Code, w.Body.String())
	}
	if w := f.request(pm, sessionPM.CsrfToken, "/api/v1/devices/SIM-P001-01/shadow", "GET"); w.Code != 404 {
		t.Fatalf("portfolio-only user read project device: %d", w.Code)
	}
}

// TestPortfolioDirectObjectExistenceLeak proves child-ID routes hide a foreign
// object's existence (404) rather than leaking it with 403, and that a caller
// with membership but insufficient capability gets 403.
func TestPortfolioDirectObjectExistenceLeak(t *testing.T) {
	f := newSecurityFixture(t)
	portfolioSecurityData(t, f)
	// A PS-B intake exists.
	if _, err := f.db.Exec(`INSERT INTO project_intakes(id,portfolio_scope_id,tenant_id,intake_code,project_name,project_type,status) VALUES('INT-B','PS-B','demo','INT-B','B Project','NEW_INSTALLATION','PLANNING')`); err != nil {
		t.Fatal(err)
	}
	var conflictID int64
	if err := f.db.QueryRow(`SELECT id FROM schedule_conflicts WHERE fingerprint='fp-b'`).Scan(&conflictID); err != nil {
		t.Fatal(err)
	}

	// user-a belongs only to PS-A: every PS-B child-ID route must be 404.
	a, sessionA := f.login(t, "a")
	if w := f.requestJSON(a, sessionA.CsrfToken, "/api/v1/portfolio/intakes/INT-B/transitions", "POST", `{"status":"REVIEW","reason":"review"}`); w.Code != 404 {
		t.Fatalf("foreign intake transition leaked existence: %d", w.Code)
	}
	if w := f.requestJSON(a, sessionA.CsrfToken, "/api/v1/portfolio/intakes/INT-B/approve", "POST", "{}"); w.Code != 404 {
		t.Fatalf("foreign intake approve leaked existence: %d", w.Code)
	}
	if w := f.request(a, sessionA.CsrfToken, "/api/v1/portfolio/resources/R-B/calendar", "GET"); w.Code != 404 {
		t.Fatalf("foreign resource calendar leaked existence: %d", w.Code)
	}
	if w := f.requestJSON(a, sessionA.CsrfToken, "/api/v1/portfolio/resources/R-B/calendar", "PUT", `{"timezone":"UTC","version":1,"rules":[]}`); w.Code != 404 {
		t.Fatalf("foreign resource calendar PUT leaked existence: %d", w.Code)
	}
	if w := f.requestJSON(a, sessionA.CsrfToken, "/api/v1/portfolio/conflicts/"+strconv.FormatInt(conflictID, 10)+"/override", "POST", `{"reason":"long enough reason"}`); w.Code != 404 {
		t.Fatalf("foreign conflict override leaked existence: %d", w.Code)
	}

	// user-b belongs to PS-B as a planner: membership exists, but approve needs
	// portfolio_manager capability -> 403, not 404.
	b, sessionB := f.login(t, "b")
	if w := f.requestJSON(b, sessionB.CsrfToken, "/api/v1/portfolio/intakes/INT-B/approve", "POST", "{}"); w.Code != 403 {
		t.Fatalf("planner approve = %d, want 403", w.Code)
	}
}
