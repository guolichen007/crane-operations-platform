package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/asset"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

func strptr(s string) *string { return &s }

func seedBusinessAsset(t *testing.T, f securityFixture, projectID string) contract.AssetMasterData {
	t.Helper()
	out, err := asset.CreateAsset(context.Background(), f.db, contract.AssetMasterData{ProjectId: projectID, AssetType: "CRANE", AssetCode: strptr("CRN-" + projectID)}, "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (f securityFixture) requestJSON(cookie *http.Cookie, csrf, path, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if stateChanging(method) {
		r.Header.Set("Origin", f.server.URL)
		r.Header.Set("X-CSRF-Token", csrf)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestAssetCoreRoutesRegistered(t *testing.T) {
	h := &apiHandler{}
	api := http.NewServeMux()
	h.registerAssets(api)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/assets"},
		{"POST", "/api/v1/assets"},
		{"GET", "/api/v1/assets/abc"},
		{"PATCH", "/api/v1/assets/abc"},
		{"POST", "/api/v1/assets/abc/retire"},
		{"POST", "/api/v1/assets/abc/move"},
		{"GET", "/api/v1/projects/p/locations"},
		{"POST", "/api/v1/projects/p/locations"},
		{"GET", "/api/v1/locations/abc"},
		{"PATCH", "/api/v1/locations/abc"},
		{"POST", "/api/v1/locations/abc/disable"},
		{"GET", "/api/v1/manufacturers"},
		{"POST", "/api/v1/manufacturers"},
		{"GET", "/api/v1/manufacturers/abc"},
		{"PATCH", "/api/v1/manufacturers/abc"},
		{"POST", "/api/v1/manufacturers/abc/disable"},
		{"GET", "/api/v1/equipment/catalog"},
		{"POST", "/api/v1/equipment/catalog"},
		{"GET", "/api/v1/equipment/catalog/abc"},
		{"PATCH", "/api/v1/equipment/catalog/abc"},
		{"POST", "/api/v1/equipment/catalog/abc/disable"},
		{"GET", "/api/v1/assets/abc/equipment"},
		{"POST", "/api/v1/assets/abc/equipment"},
		{"GET", "/api/v1/equipment/abc"},
		{"PATCH", "/api/v1/equipment/abc"},
		{"POST", "/api/v1/equipment/abc/remove"},
		{"GET", "/api/v1/assets/abc/equipment-relations"},
		{"POST", "/api/v1/assets/abc/equipment-relations"},
		{"POST", "/api/v1/equipment-relations/abc/remove"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		_, pattern := api.Handler(req)
		if pattern == "" {
			t.Fatalf("asset route not registered: %s %s", tc.method, tc.path)
		}
	}
}

func TestAssetHTTPAuthorizationMatrix(t *testing.T) {
	f := newSecurityFixture(t)
	p1 := seedBusinessAsset(t, f, "P001")
	seedBusinessAsset(t, f, "P002")

	viewer, sv := f.login(t, "a")
	maintainer, sm := f.login(t, "maintainer")
	admin, sa := f.login(t, "admin")

	// Viewer: read own, but no asset write.
	if w := f.request(viewer, sv.CsrfToken, "/api/v1/assets/"+p1.Id, "GET"); w.Code != 200 {
		t.Fatalf("viewer own read = %d", w.Code)
	}
	if w := f.requestJSON(viewer, sv.CsrfToken, "/api/v1/assets", "POST", `{"projectId":"P001","assetType":"CRANE"}`); w.Code != 403 {
		t.Fatalf("viewer write = %d, want 403", w.Code)
	}
	// Maintainer: no asset master write.
	if w := f.requestJSON(maintainer, sm.CsrfToken, "/api/v1/assets", "POST", `{"projectId":"P001","assetType":"CRANE"}`); w.Code != 403 {
		t.Fatalf("maintainer write = %d, want 403", w.Code)
	}
	// platform_admin WITH ProjectGrant: can business write.
	if w := f.requestJSON(admin, sa.CsrfToken, "/api/v1/assets", "POST", `{"projectId":"P001","assetType":"CRANE"}`); w.Code != 201 {
		t.Fatalf("admin write = %d, want 201", w.Code)
	}
	// platform_admin (P001 only) cannot read a P002 asset.
	var p2id string
	if err := f.db.QueryRow(`SELECT id FROM asset_devices WHERE project_id='P002' LIMIT 1`).Scan(&p2id); err != nil {
		t.Fatal(err)
	}
	if w := f.request(admin, sa.CsrfToken, "/api/v1/assets/"+p2id, "GET"); w.Code != 404 {
		t.Fatalf("admin cross-project read = %d, want 404", w.Code)
	}
}

func TestAssetHTTPPlatformAdminWithoutProjectGrantHidden(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	// A platform_admin identity with NO project grant.
	if err := identity.Bind(ctx, f.db, "user-nogrant", f.provider.Server.URL, "subject-nogrant", "No Grant Admin", true, auditlog.Event{Actor: "fixture"}); err != nil {
		t.Fatal(err)
	}
	cookie, session := f.login(t, "nogrant")
	if w := f.request(cookie, session.CsrfToken, "/api/v1/assets", "GET"); w.Code != 200 {
		t.Fatalf("no-grant list = %d", w.Code)
	} else if strings.Contains(w.Body.String(), "CRN-P001") || strings.Contains(w.Body.String(), "CRN-P002") {
		t.Fatalf("no-grant admin leaked assets: %s", w.Body.String())
	}
	p1 := seedBusinessAsset(t, f, "P001")
	if w := f.request(cookie, session.CsrfToken, "/api/v1/assets/"+p1.Id, "GET"); w.Code != 404 {
		t.Fatalf("no-grant admin read = %d, want 404", w.Code)
	}
}
