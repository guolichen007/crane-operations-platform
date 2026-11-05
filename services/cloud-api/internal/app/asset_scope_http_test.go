package app

import (
	"net/http"
	"strings"
	"testing"
)

// The top command bar's ?project= scope must narrow the asset ledger to an
// authorized project only: ?project=P001 never shows P002 data, and a project
// the caller cannot read yields an empty list (no leak).
func TestHTTPAssetProjectScopeFilter(t *testing.T) {
	f := newSecurityFixture(t)
	admin, info := f.login(t, "admin") // platform_admin in P001 only

	// A P002 asset exists but admin holds no P002 grant.
	if _, err := f.db.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_code,asset_type,asset_status,version) VALUES('p002-scope-asset','P002','P002 scope asset','PLANNED','CRN-P002-SCOPE','CRANE','ACTIVE',1)`); err != nil {
		t.Fatal(err)
	}
	create := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-P001-SCOPE"})
	if create.Code != http.StatusCreated {
		t.Fatalf("asset create = %d (%s)", create.Code, create.Body.String())
	}

	// Scoped to P001: contains the P001 asset, never the P002 one.
	p1 := f.doBody(admin, "", "GET", "/api/v1/assets?project=P001", nil, nil)
	if p1.Code != http.StatusOK || !strings.Contains(p1.Body.String(), "CRN-P001-SCOPE") || strings.Contains(p1.Body.String(), "CRN-P002-SCOPE") {
		t.Fatalf("P001 scope = %d, leaked or missing: %s", p1.Code, p1.Body.String())
	}

	// Scoped to P002 (unauthorized): empty, never the P002 asset.
	p2 := f.doBody(admin, "", "GET", "/api/v1/assets?project=P002", nil, nil)
	if p2.Code != http.StatusOK || strings.Contains(p2.Body.String(), "CRN-P002-SCOPE") {
		t.Fatalf("P002 scope leaked data: %d %s", p2.Code, p2.Body.String())
	}
	if strings.TrimSpace(p2.Body.String()) != "[]" {
		t.Fatalf("P002 scope should be empty array, got %s", p2.Body.String())
	}
}

// The environment sentinel is server-side: unset (as in production) it reports
// "", so a Preview UAT pointed at production refuses to write; set, it reports
// the configured name. It requires an authenticated session.
func TestHTTPEnvironmentSentinel(t *testing.T) {
	t.Setenv("CRANE_DEPLOYMENT_ENVIRONMENT", "")
	f := newSecurityFixture(t)
	admin, _ := f.login(t, "admin")
	if w := f.doBody(nil, "", "GET", "/api/v1/environment", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous sentinel = %d, want 401", w.Code)
	}
	w := f.doBody(admin, "", "GET", "/api/v1/environment", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"environment":""`) {
		t.Fatalf("unset sentinel = %d %s, want empty environment", w.Code, w.Body.String())
	}

	t.Setenv("CRANE_DEPLOYMENT_ENVIRONMENT", "b4-preview")
	g := newSecurityFixture(t)
	admin2, _ := g.login(t, "admin")
	w = g.doBody(admin2, "", "GET", "/api/v1/environment", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"environment":"b4-preview"`) {
		t.Fatalf("set sentinel = %d %s, want b4-preview", w.Code, w.Body.String())
	}
}

// An over-long displayName is a client error (422), not a 503, and is not stored.
func TestHTTPAssetDisplayNameTooLong(t *testing.T) {
	f := newSecurityFixture(t)
	admin, info := f.login(t, "admin")
	w := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/assets", nil,
		map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-LONG-HTTP", "displayName": strings.Repeat("机", 161)})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-long displayName = %d, want 422 (%s)", w.Code, w.Body.String())
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM asset_devices WHERE asset_code='CRN-LONG-HTTP'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected create stored %d rows (err=%v)", n, err)
	}
}
