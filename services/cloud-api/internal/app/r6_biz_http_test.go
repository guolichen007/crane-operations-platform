package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
)

// R6-BIZ-R1 through the real BFF (OIDC session, CSRF, project-scope-first): the
// F-02 empty-directory refusal and bootstrap, the disabled-type guard, and the
// F-03 location readback with cross-project isolation.

func decodeJSON[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", string(b), err)
	}
	return v
}

func TestR6EquipmentTypeDirectoryHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	admin, ai := f.login(t, "admin")
	create := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-R6-TYPES"})
	if create.Code != http.StatusCreated {
		t.Fatalf("asset create = %d %s", create.Code, create.Body.String())
	}
	a := decodeJSON[contract.AssetMasterData](t, create.Body.Bytes())
	install := func(code, typeCode string, catalog *string) *http.Response {
		body := map[string]any{"equipmentCode": code, "typeCode": typeCode, "criticality": "MEDIUM", "administrativeState": "IN_SERVICE", "spec": map[string]any{}}
		if catalog != nil {
			body["catalogItemId"] = *catalog
		}
		return f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets/"+a.Id+"/equipment", nil, body).Result()
	}

	// 1. Fresh schema-12 directory: no installable type, the dictionary is an honest empty list,
	//    and a direct API install is refused as invalid input (not 503, not 201).
	types := f.request(admin, ai.CsrfToken, "/api/v1/equipment/types", "GET")
	if types.Code != 200 || strings.TrimSpace(types.Body.String()) != "[]" {
		t.Fatalf("types on empty directory = %d %s, want 200 []", types.Code, types.Body.String())
	}
	if res := install("EQ-0", "MOTOR", nil); res.StatusCode != 422 {
		t.Fatalf("install on empty directory = %d, want 422", res.StatusCode)
	}

	// 2. Bootstrap MOTOR + PLC through the controlled initializer (plan → approved apply).
	scope, _ := equipmenttypes.Scope(false, []string{"MOTOR", "PLC"})
	plan, err := equipmenttypes.PlanTypes(t.Context(), f.db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := equipmenttypes.Apply(t.Context(), f.db, scope, plan.Digest, "ops", "r6-apply"); err != nil {
		t.Fatal(err)
	}
	listed := decodeJSON[[]contract.EquipmentType](t, f.request(admin, ai.CsrfToken, "/api/v1/equipment/types", "GET").Body.Bytes())
	if len(listed) != 2 || listed[0].TypeCode != "MOTOR" || listed[0].DisplayName != "电机" {
		t.Fatalf("types after bootstrap = %+v", listed)
	}
	if res := install("EQ-1", "MOTOR", nil); res.StatusCode != 201 {
		t.Fatalf("install after bootstrap = %d", res.StatusCode)
	}

	// 3. Client-submitted codes outside the registry are refused; the HTTP surface has no type
	//    write route at all.
	if res := install("EQ-2", "ROCKET", nil); res.StatusCode != 422 {
		t.Fatalf("unregistered type = %d, want 422", res.StatusCode)
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if w := f.doBody(admin, ai.CsrfToken, m, "/api/v1/equipment/types", nil, map[string]any{"typeCode": "ROCKET"}); w.Code != 405 && w.Code != 404 {
			t.Fatalf("%s /equipment/types = %d, want no write route", m, w.Code)
		}
	}

	// 4. Disable PLC: no longer listed, direct install refused (422), also through an ACTIVE
	//    catalog; the MOTOR installation is still updatable.
	cat := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/equipment/catalog", nil, map[string]any{"projectId": "P001", "name": "PLC-CAT", "typeCode": "PLC", "brand": "", "model": "", "productCode": "", "specSchemaVersion": "1", "status": "ACTIVE", "id": "", "manufacturerId": nil, "version": 0})
	if cat.Code != http.StatusCreated {
		t.Fatalf("catalog create = %d %s", cat.Code, cat.Body.String())
	}
	catalog := decodeJSON[contract.EquipmentCatalogItem](t, cat.Body.Bytes())
	if _, err := f.db.Exec(`UPDATE equipment_types SET status='DISABLED', version=version+1 WHERE type_code='PLC'`); err != nil {
		t.Fatal(err)
	}
	listed = decodeJSON[[]contract.EquipmentType](t, f.request(admin, ai.CsrfToken, "/api/v1/equipment/types", "GET").Body.Bytes())
	if len(listed) != 1 || listed[0].TypeCode != "MOTOR" {
		t.Fatalf("types after disabling PLC = %+v", listed)
	}
	if res := install("EQ-3", "PLC", nil); res.StatusCode != 422 {
		t.Fatalf("disabled type direct install = %d, want 422", res.StatusCode)
	}
	if res := install("EQ-4", "PLC", &catalog.Id); res.StatusCode != 422 {
		t.Fatalf("disabled type via active catalog = %d, want 422", res.StatusCode)
	}
	var plcInstalls int
	if err := f.db.QueryRow(`SELECT count(*) FROM asset_equipment_installations WHERE type_code='PLC'`).Scan(&plcInstalls); err != nil || plcInstalls != 0 {
		t.Fatalf("PLC installations = %d (err=%v)", plcInstalls, err)
	}
	// 5. Duplicate equipment code on the same asset is a 422 input error, not a 503.
	if res := install("EQ-1", "MOTOR", nil); res.StatusCode != 422 {
		t.Fatalf("duplicate equipment code = %d, want 422", res.StatusCode)
	}
}

func TestR6AssetLocationReadbackHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	admin, ai := f.login(t, "admin")
	viewer, vi := f.login(t, "a") // P001 viewer
	other, oi := f.login(t, "b")  // P002 operator
	create := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-R6-LOC"})
	if create.Code != http.StatusCreated {
		t.Fatalf("asset create = %d %s", create.Code, create.Body.String())
	}
	a := decodeJSON[contract.AssetMasterData](t, create.Body.Bytes())
	get := func(c *http.Cookie, csrf, path string) (int, string) {
		w := f.request(c, csrf, path, "GET")
		return w.Code, w.Body.String()
	}

	// Never installed: honest empty state for any reader of the project.
	for _, who := range []struct {
		c    *http.Cookie
		csrf string
	}{{admin, ai.CsrfToken}, {viewer, vi.CsrfToken}} {
		code, body := get(who.c, who.csrf, "/api/v1/assets/"+a.Id+"/location")
		if code != 200 {
			t.Fatalf("location = %d %s", code, body)
		}
		rb := decodeJSON[contract.AssetLocationReadback](t, []byte(body))
		if rb.Current != nil || !strings.Contains(body, `"current":null`) {
			t.Fatalf("never-installed readback = %s", body)
		}
		if strings.Contains(body, "longitude") || strings.Contains(body, "latitude") {
			t.Fatalf("readback carries coordinates: %s", body)
		}
	}
	code, body := get(viewer, vi.CsrfToken, "/api/v1/assets/"+a.Id+"/installation-history")
	if code != 200 || !strings.Contains(body, `"total":0`) || !strings.Contains(body, `"items":[]`) {
		t.Fatalf("empty history = %d %s", code, body)
	}

	// Move through the BFF into a SITE → WORKSHOP created through the BFF.
	post := func(path string, headers map[string]string, b any) (int, string) {
		w := f.doBody(admin, ai.CsrfToken, "POST", path, headers, b)
		return w.Code, w.Body.String()
	}
	_, siteBody := post("/api/v1/projects/P001/locations", nil, map[string]any{"kind": "SITE", "name": "R6 场地", "code": "R6-S"})
	site := decodeJSON[contract.AssetLocation](t, []byte(siteBody))
	_, wsBody := post("/api/v1/projects/P001/locations", nil, map[string]any{"kind": "WORKSHOP", "name": "R6 车间", "code": "R6-W", "parentId": site.Id})
	ws := decodeJSON[contract.AssetLocation](t, []byte(wsBody))
	if c, b := post("/api/v1/assets/"+a.Id+"/move", map[string]string{"If-Match": strconv.FormatInt(a.Version, 10)}, map[string]any{"locationId": ws.Id}); c != 200 {
		t.Fatalf("move = %d %s", c, b)
	}
	// A stale If-Match on a second move is 409 and leaves the current location unchanged.
	if c, b := post("/api/v1/assets/"+a.Id+"/move", map[string]string{"If-Match": strconv.FormatInt(a.Version, 10)}, map[string]any{"locationId": site.Id}); c != 409 {
		t.Fatalf("stale move = %d %s, want 409", c, b)
	}
	code, body = get(viewer, vi.CsrfToken, "/api/v1/assets/"+a.Id+"/location")
	rb := decodeJSON[contract.AssetLocationReadback](t, []byte(body))
	if code != 200 || rb.Current == nil || rb.Current.LocationId != ws.Id || rb.Current.Kind != "WORKSHOP" || len(rb.Current.Path) != 2 || rb.Current.Path[0].Id != site.Id {
		t.Fatalf("location after move = %d %s", code, body)
	}
	if w := f.request(viewer, vi.CsrfToken, "/api/v1/assets/"+a.Id+"/location", "GET"); w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("location readback is cacheable")
	}
	code, body = get(viewer, vi.CsrfToken, "/api/v1/assets/"+a.Id+"/installation-history?limit=10&offset=0")
	hist := decodeJSON[contract.AssetInstallationHistory](t, []byte(body))
	if code != 200 || hist.Total != 1 || len(hist.Items) != 1 || !hist.Items[0].Current || hist.Items[0].LocationId != ws.Id {
		t.Fatalf("history after move = %d %s", code, body)
	}
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=x", "?offset=-1"} {
		if c, _ := get(viewer, vi.CsrfToken, "/api/v1/assets/"+a.Id+"/installation-history"+q); c != 400 {
			t.Fatalf("history%s = %d, want 400", q, c)
		}
	}

	// Cross-project: a P002 principal gets 404 for P001's asset (no body leak) on both routes,
	// and so does an unknown asset id; the denial is audited like every project-scope denial.
	for _, path := range []string{"/api/v1/assets/" + a.Id + "/location", "/api/v1/assets/" + a.Id + "/installation-history"} {
		c, b := get(other, oi.CsrfToken, path)
		if c != 404 || strings.Contains(b, "P001") || strings.Contains(b, ws.Id) {
			t.Fatalf("cross-project %s = %d %s, want bare 404", path, c, b)
		}
		if c, _ := get(other, oi.CsrfToken, strings.Replace(path, a.Id, "no-such-asset", 1)); c != 404 {
			t.Fatalf("unknown asset %s = %d", path, c)
		}
		if c, _ := get(nil, "", path); c != 401 {
			t.Fatalf("anonymous %s = %d, want 401", path, c)
		}
	}
	var denies int
	if err := f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE user_id='user-b' AND action='authorization_denied' AND project_id='P001'`).Scan(&denies); err != nil || denies < 2 {
		t.Fatalf("cross-project location denials audited = %d (err=%v)", denies, err)
	}
	// No write route under the readback paths.
	for _, path := range []string{"/api/v1/assets/" + a.Id + "/location", "/api/v1/assets/" + a.Id + "/installation-history"} {
		if w := f.doBody(admin, ai.CsrfToken, "POST", path, nil, map[string]any{}); w.Code != 405 && w.Code != 404 {
			t.Fatalf("POST %s = %d, want no write route", path, w.Code)
		}
	}

	// Retired asset: readback and history remain readable.
	var version int64
	if err := f.db.QueryRow(`SELECT version FROM asset_devices WHERE id=$1`, a.Id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if c, b := post("/api/v1/assets/"+a.Id+"/retire", map[string]string{"If-Match": strconv.FormatInt(version, 10)}, map[string]any{}); c != 200 {
		t.Fatalf("retire = %d %s", c, b)
	}
	code, body = get(viewer, vi.CsrfToken, "/api/v1/assets/"+a.Id+"/location")
	rb = decodeJSON[contract.AssetLocationReadback](t, []byte(body))
	if code != 200 || rb.AssetStatus != "RETIRED" || rb.Current == nil || rb.Current.LocationId != ws.Id {
		t.Fatalf("retired readback = %d %s", code, body)
	}
}

// R6-BIZ-R1 F-08 over the BFF: a cyclic / self / foreign parent on PATCH is 422 (not 503), a
// legal re-parent is 200.
func TestR6LocationParentGuardHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	admin, ai := f.login(t, "admin")
	mk := func(body map[string]any) contract.AssetLocation {
		w := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/projects/P001/locations", nil, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("create = %d %s", w.Code, w.Body.String())
		}
		return decodeJSON[contract.AssetLocation](t, w.Body.Bytes())
	}
	a := mk(map[string]any{"kind": "SITE", "name": "F08-A"})
	b := mk(map[string]any{"kind": "PLANT", "name": "F08-B", "parentId": a.Id})
	other := mk(map[string]any{"kind": "SITE", "name": "F08-O"})
	patch := func(l contract.AssetLocation, parent *string) (int, string) {
		l.ParentId = parent
		w := f.doBody(admin, ai.CsrfToken, "PATCH", "/api/v1/locations/"+l.Id, map[string]string{"If-Match": strconv.FormatInt(l.Version, 10)}, l)
		return w.Code, w.Body.String()
	}
	if c, body := patch(a, &a.Id); c != 422 {
		t.Fatalf("self parent = %d %s", c, body)
	}
	if c, body := patch(a, &b.Id); c != 422 {
		t.Fatalf("cycle = %d %s", c, body)
	}
	p2 := "q4-none"
	if c, body := patch(b, &p2); c != 422 {
		t.Fatalf("unknown parent = %d %s", c, body)
	}
	if c, body := patch(b, &other.Id); c != 200 {
		t.Fatalf("legal re-parent = %d %s", c, body)
	}
}
