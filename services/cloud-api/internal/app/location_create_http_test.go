package app

import (
	"encoding/json"
	"net/http"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

// Q4-QA-R1 §4: the existing Location create API distinguishes user input errors (422), a duplicate
// code (409) and authorization (403) from a database outage (503), keeps coordinates UNKNOWN when
// none are given, and the created location is a legal move target for an asset of the same project.
func TestHTTPLocationCreateValidationAndMove(t *testing.T) {
	f := newSecurityFixture(t)
	admin, ai := f.login(t, "admin")
	viewer, vi := f.login(t, "a")
	post := func(c *http.Cookie, csrf, project, body string) (int, contract.AssetLocation, string) {
		w := f.doBody(c, csrf, "POST", "/api/v1/projects/"+project+"/locations", nil, raw(body))
		var l contract.AssetLocation
		_ = json.Unmarshal(w.Body.Bytes(), &l)
		return w.Code, l, w.Body.String()
	}

	code, site, body := post(admin, ai.CsrfToken, "P001", `{"kind":"SITE","name":"  一号场地 ","code":"Q4-S1"}`)
	if code != http.StatusCreated {
		t.Fatalf("create site = %d %s", code, body)
	}
	if site.Name != "一号场地" || site.Status != "ACTIVE" || site.CoordinateSystem != "UNKNOWN" || site.Longitude != nil || site.Latitude != nil {
		t.Fatalf("created site = %+v, want trimmed name, ACTIVE, UNKNOWN, no coordinates", site)
	}
	code, ws, body := post(admin, ai.CsrfToken, "P001", `{"kind":"WORKSHOP","name":"一车间","parentId":"`+site.Id+`"}`)
	if code != http.StatusCreated || ws.ParentId == nil || *ws.ParentId != site.Id {
		t.Fatalf("create child = %d %s", code, body)
	}

	for name, c := range map[string]struct {
		body string
		want int
	}{
		"empty name":                 {`{"kind":"SITE","name":"  "}`, 422},
		"unknown kind":               {`{"kind":"CITY","name":"x"}`, 422},
		"half coordinates":           {`{"kind":"SITE","name":"x","longitude":120.1}`, 422},
		"WGS84 without coordinates":  {`{"kind":"SITE","name":"x","coordinateSystem":"WGS84"}`, 422},
		"coordinates without system": {`{"kind":"SITE","name":"x","longitude":120.1,"latitude":30.2}`, 422},
		"unknown parent":             {`{"kind":"BAY","name":"x","parentId":"nope"}`, 422},
		"disabled on create":         {`{"kind":"SITE","name":"x","status":"DISABLED"}`, 422},
		"duplicate code":             {`{"kind":"SITE","name":"另一场地","code":"Q4-S1"}`, 409},
		"unknown field":              {`{"kind":"SITE","name":"x","lng":1}`, 400},
	} {
		if got, _, b := post(admin, ai.CsrfToken, "P001", c.body); got != c.want {
			t.Errorf("%s = %d %s, want %d", name, got, b, c.want)
		}
	}
	// A parent of another project is "not found in this project", never a cross-project write.
	var p2 string
	if err := f.db.QueryRow(`INSERT INTO asset_locations(id,project_id,kind,name) VALUES('q4-p2-site','P002','SITE','P2') RETURNING id`).Scan(&p2); err != nil {
		t.Fatal(err)
	}
	if got, _, b := post(admin, ai.CsrfToken, "P001", `{"kind":"BAY","name":"x","parentId":"`+p2+`"}`); got != 422 {
		t.Errorf("foreign parent = %d %s, want 422", got, b)
	}
	// Authorization is unchanged: a viewer (no location_write) and another project are refused.
	if got, _, _ := post(viewer, vi.CsrfToken, "P001", `{"kind":"SITE","name":"x"}`); got != http.StatusForbidden {
		t.Errorf("viewer create = %d, want 403", got)
	}
	if got, _, _ := post(admin, ai.CsrfToken, "P002", `{"kind":"SITE","name":"x"}`); got != http.StatusForbidden && got != http.StatusNotFound {
		t.Errorf("admin create in P002 = %d, want 403/404", got)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM asset_locations WHERE project_id='P001'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("P001 locations = %d (%v), want exactly the 2 legal ones", n, err)
	}

	// The new location is a legal move target.
	w := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", nil, raw(`{"projectId":"P001","assetType":"CRANE","assetCode":"Q4-LOC-1"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create asset = %d", w.Code)
	}
	var a contract.AssetMasterData
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if w = f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets/"+a.Id+"/move", ifMatch(a.Version), raw(`{"locationId":"`+ws.Id+`"}`)); w.Code != http.StatusOK {
		t.Fatalf("move = %d %s", w.Code, w.Body.String())
	}
	var at string
	if err := f.db.QueryRow(`SELECT location_id FROM asset_installations WHERE asset_id=$1 AND valid_to IS NULL`, a.Id).Scan(&at); err != nil || at != ws.Id {
		t.Fatalf("current installation = %q (%v), want %s", at, err, ws.Id)
	}
}
