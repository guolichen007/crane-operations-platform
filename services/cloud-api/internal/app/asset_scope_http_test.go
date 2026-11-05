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
