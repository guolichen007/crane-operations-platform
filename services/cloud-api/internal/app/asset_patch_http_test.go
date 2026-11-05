package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

// raw sends the exact JSON text (json.RawMessage marshals verbatim), so tests can
// distinguish an omitted key from an explicit null.
func raw(s string) json.RawMessage { return json.RawMessage(s) }

func ifMatch(v int64) map[string]string {
	return map[string]string{"If-Match": strconv.FormatInt(v, 10)}
}

type patchFixture struct {
	f      securityFixture
	cookie *http.Cookie
	csrf   string
}

func newPatchFixture(t *testing.T) patchFixture {
	t.Helper()
	f := newSecurityFixture(t)
	cookie, info := f.login(t, "admin")
	if _, err := f.db.Exec(`INSERT INTO manufacturers(id,project_id,name,short_name,status,version) VALUES('mfr-p001','P001','一号厂','一号厂','ACTIVE',1),('mfr-p002','P002','二号厂','二号厂','ACTIVE',1)`); err != nil {
		t.Fatal(err)
	}
	return patchFixture{f, cookie, info.CsrfToken}
}

// fullAsset creates an asset with every business field populated.
func (p patchFixture) fullAsset(t *testing.T, code string) contract.AssetMasterData {
	t.Helper()
	body := `{"projectId":"P001","displayName":"满字段资产","assetCode":"` + code + `","assetType":"GANTRY",
	  "manufacturerId":"mfr-p001","manufacturerSnapshot":"一号厂","brandSnapshot":"品牌A","modelSnapshot":"MG-50",
	  "serialNumber":"SN-001","manufactureDate":"2020-01-02","installationDate":"2021-03-04","commissioningDate":"2021-05-06",
	  "liftingHeightM":12.5,"applicationType":"港口","controlMode":"遥控","specialEquipmentCode":"SE-1","useRegistrationNo":"UR-1"}`
	w := p.f.doBody(p.cookie, p.csrf, "POST", "/api/v1/assets", nil, raw(body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var a contract.AssetMasterData
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func (p patchFixture) get(t *testing.T, id string) contract.AssetMasterData {
	t.Helper()
	w := p.f.doBody(p.cookie, "", "GET", "/api/v1/assets/"+id, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get = %d", w.Code)
	}
	var a contract.AssetMasterData
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func s(v *string) string {
	if v == nil {
		return "<nil>"
	}
	return *v
}

// PATCH {"assetCode":"NEW"} must leave every other business field untouched.
func TestHTTPAssetPatchPartialPreservesFields(t *testing.T) {
	p := newPatchFixture(t)
	before := p.fullAsset(t, "CRN-PATCH-1")

	w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+before.Id, ifMatch(before.Version), raw(`{"assetCode":"CRN-PATCH-NEW"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("partial patch = %d %s", w.Code, w.Body.String())
	}
	after := p.get(t, before.Id)
	if s(after.AssetCode) != "CRN-PATCH-NEW" || after.Version != before.Version+1 {
		t.Fatalf("assetCode/version not applied: %s v%d", s(after.AssetCode), after.Version)
	}
	// Every non-provided field is byte-for-byte the pre-patch value.
	expected := before
	expected.AssetCode = after.AssetCode
	expected.Version = after.Version
	gotJSON, _ := json.Marshal(after)
	wantJSON, _ := json.Marshal(expected)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("partial PATCH changed other fields:\n got  %s\n want %s", gotJSON, wantJSON)
	}

	// The revision records exactly the provided field, and the snapshot is the stored row.
	var changed, snapshot string
	if err := p.f.db.QueryRow(`SELECT changed_fields::text, snapshot_json::text FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1 ORDER BY revision DESC LIMIT 1`, before.Id).Scan(&changed, &snapshot); err != nil {
		t.Fatal(err)
	}
	if changed != "{updated,assetCode}" {
		t.Fatalf("changed_fields = %s, want {updated,assetCode}", changed)
	}
	for _, want := range []string{`"GANTRY"`, `"MG-50"`, `"2020-01-02"`, `"CRN-PATCH-NEW"`, `"满字段资产"`} {
		if !strings.Contains(snapshot, want) {
			t.Fatalf("revision snapshot missing %s: %s", want, snapshot)
		}
	}
}

// Explicit null clears nullable fields; clearing required fields is 422 with no
// DB change; unknown keys and identity/ownership changes are rejected.
func TestHTTPAssetPatchNullAndRequiredSemantics(t *testing.T) {
	p := newPatchFixture(t)
	a := p.fullAsset(t, "CRN-PATCH-2")

	w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version),
		raw(`{"serialNumber":null,"manufactureDate":null,"liftingHeightM":null,"manufacturerId":null}`))
	if w.Code != http.StatusOK {
		t.Fatalf("null clear = %d %s", w.Code, w.Body.String())
	}
	cleared := p.get(t, a.Id)
	if cleared.SerialNumber != nil || cleared.ManufactureDate != nil || cleared.LiftingHeightM != nil || cleared.ManufacturerId != nil {
		t.Fatalf("nullable fields not cleared: %+v", cleared)
	}
	if s(cleared.InstallationDate) != "2021-03-04" || cleared.AssetType != "GANTRY" || cleared.ModelSnapshot != "MG-50" {
		t.Fatalf("null clear touched other fields: %+v", cleared)
	}

	reject := func(name, body string, status int) {
		t.Helper()
		before := p.get(t, a.Id)
		w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(before.Version), raw(body))
		if w.Code != status {
			t.Fatalf("%s = %d, want %d (%s)", name, w.Code, status, w.Body.String())
		}
		after := p.get(t, a.Id)
		if after.Version != before.Version {
			t.Fatalf("%s changed the row (v%d → v%d)", name, before.Version, after.Version)
		}
	}
	reject("clear assetType (null)", `{"assetType":null}`, 422)
	reject("clear assetType (blank)", `{"assetType":"   "}`, 422)
	reject("clear displayName (null)", `{"displayName":null}`, 422)
	reject("blank displayName", `{"displayName":"  "}`, 422)
	reject("invalid date", `{"installationDate":"2021-13-45"}`, 422)
	reject("negative height", `{"liftingHeightM":-1}`, 422)
	reject("unknown status", `{"assetStatus":"BROKEN"}`, 422)
	reject("retire via patch", `{"assetStatus":"RETIRED"}`, 409)
	reject("move project", `{"projectId":"P002"}`, 422)
	reject("change id", `{"id":"other"}`, 422)
	reject("body version mismatch", `{"version":999,"assetCode":"X"}`, 422)
	reject("unknown key (telemetry)", `{"runtimeDeviceId":"SIM-1"}`, 400)
	reject("unknown key", `{"connectivity":"ONLINE"}`, 400)
	reject("missing manufacturer", `{"manufacturerId":"mfr-missing"}`, 422)
	reject("cross-project manufacturer", `{"manufacturerId":"mfr-p002"}`, 422)

	// Echoing the unchanged identity fields (legacy whole-object clients) is fine.
	cur := p.get(t, a.Id)
	echo := `{"id":"` + cur.Id + `","projectId":"P001","version":` + strconv.FormatInt(cur.Version, 10) + `,"brandSnapshot":"品牌B"}`
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(cur.Version), raw(echo)); w.Code != http.StatusOK {
		t.Fatalf("identity echo = %d %s", w.Code, w.Body.String())
	}
}

// displayName: 160 chars accepted, 161 rejected (422, no DB change) on CREATE and
// PATCH; whitespace-only PATCH is 422; a rejected create carrying an
// Idempotency-Key does not consume the key — a corrected retry with the same key
// creates the asset.
func TestHTTPAssetDisplayNameBoundaryAndIdempotency(t *testing.T) {
	p := newPatchFixture(t)
	name := func(n int) string { return strings.Repeat("机", n) }
	create := func(key, display, code string) int {
		t.Helper()
		body := `{"projectId":"P001","assetType":"CRANE","assetCode":"` + code + `","displayName":"` + display + `"}`
		return p.f.doBody(p.cookie, p.csrf, "POST", "/api/v1/assets", map[string]string{"Idempotency-Key": key}, raw(body)).Code
	}
	count := func(code string) int {
		var n int
		_ = p.f.db.QueryRow(`SELECT count(*) FROM asset_devices WHERE asset_code=$1`, code).Scan(&n)
		return n
	}

	if code := create("idem-161", name(161), "CRN-IDEM-161"); code != 422 || count("CRN-IDEM-161") != 0 {
		t.Fatalf("161-char create = %d, rows=%d; want 422, 0", code, count("CRN-IDEM-161"))
	}
	// Same key, corrected payload: validation runs before the key is claimed.
	if code := create("idem-161", name(160), "CRN-IDEM-161"); code != 201 || count("CRN-IDEM-161") != 1 {
		t.Fatalf("corrected retry with same key = %d, rows=%d; want 201, 1", code, count("CRN-IDEM-161"))
	}
	var keys int
	_ = p.f.db.QueryRow(`SELECT count(*) FROM idempotency_keys WHERE key='idem-161'`).Scan(&keys)
	if keys != 1 {
		t.Fatalf("idempotency rows = %d, want 1", keys)
	}

	a := p.fullAsset(t, "CRN-PATCH-3")
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version), raw(`{"displayName":"`+name(161)+`"}`)); w.Code != 422 {
		t.Fatalf("161-char patch = %d, want 422", w.Code)
	}
	if got := p.get(t, a.Id); got.Version != a.Version || got.DisplayName != a.DisplayName {
		t.Fatalf("rejected patch changed row: v%d %q", got.Version, got.DisplayName)
	}
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version), raw(`{"displayName":"`+name(160)+`"}`)); w.Code != 200 {
		t.Fatalf("160-char patch = %d, want 200", w.Code)
	}
}

// Two editors open v1. Editor A saves (v2). Editor B saves with stale If-Match
// v1 → 409; the stored row keeps A's content and exactly one success audit exists
// for A's write; B produces no revision.
func TestHTTPAssetPatchStaleEditorConflict(t *testing.T) {
	p := newPatchFixture(t)
	a := p.fullAsset(t, "CRN-PATCH-4")
	headers := func(req string) map[string]string {
		h := ifMatch(a.Version)
		h["X-Request-Id"] = req
		return h
	}
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, headers("req-editor-a"), raw(`{"brandSnapshot":"A的修改"}`)); w.Code != 200 {
		t.Fatalf("editor A = %d", w.Code)
	}
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, headers("req-editor-b"), raw(`{"brandSnapshot":"B的旧草稿"}`)); w.Code != http.StatusConflict {
		t.Fatalf("stale editor B = %d, want 409", w.Code)
	}
	got := p.get(t, a.Id)
	if got.BrandSnapshot != "A的修改" || got.Version != a.Version+1 {
		t.Fatalf("stored = %q v%d, want A的修改 v%d", got.BrandSnapshot, got.Version, a.Version+1)
	}
	var auditsA, revB int
	_ = p.f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE request_id='req-editor-a' AND action='asset_updated' AND result='success'`).Scan(&auditsA)
	_ = p.f.db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE request_id='req-editor-b'`).Scan(&revB)
	if auditsA != 1 || revB != 0 {
		t.Fatalf("audit A=%d (want 1), revisions B=%d (want 0)", auditsA, revB)
	}
}
