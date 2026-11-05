package app

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// counts returns (revisions, success update audits) for an asset.
func (p patchFixture) counts(t *testing.T, id string) (int, int) {
	t.Helper()
	var revs, audits int
	if err := p.f.db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1`, id).Scan(&revs); err != nil {
		t.Fatal(err)
	}
	if err := p.f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='asset_updated' AND result='success'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	return revs, audits
}

// A PATCH that changes nothing is an idempotent no-op: 200 with the current
// entity, same version and ETag, byte-identical row, and no revision or success
// audit. Change detection compares stored values, not key presence.
func TestHTTPAssetPatchNoopKeepsVersionAndAudit(t *testing.T) {
	p := newPatchFixture(t)
	a := p.fullAsset(t, "CRN-NOOP-1")
	// A second asset whose assetCode and dates are already NULL.
	w := p.f.doBody(p.cookie, p.csrf, "POST", "/api/v1/assets", nil, raw(`{"projectId":"P001","assetType":"CRANE","displayName":"空编码"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create null-code asset = %d %s", w.Code, w.Body.String())
	}
	var nullCode struct {
		Id      string
		Version int64
	}
	_ = json.Unmarshal(w.Body.Bytes(), &nullCode)

	noop := func(id string, version int64, body string) {
		t.Helper()
		before := p.get(t, id)
		beforeJSON, _ := json.Marshal(before)
		revs, audits := p.counts(t, id)
		w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+id, ifMatch(version), raw(body))
		if w.Code != http.StatusOK {
			t.Fatalf("no-op %s = %d %s", body, w.Code, w.Body.String())
		}
		if got := w.Header().Get("ETag"); got != `"`+itoa(before.Version)+`"` {
			t.Fatalf("no-op %s ETag = %s, want \"%d\"", body, got, before.Version)
		}
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if int64(resp["version"].(float64)) != before.Version {
			t.Fatalf("no-op %s response version = %v, want %d", body, resp["version"], before.Version)
		}
		after := p.get(t, id)
		afterJSON, _ := json.Marshal(after)
		if string(afterJSON) != string(beforeJSON) {
			t.Fatalf("no-op %s changed the row:\n before %s\n after  %s", body, beforeJSON, afterJSON)
		}
		r2, a2 := p.counts(t, id)
		if r2 != revs || a2 != audits {
			t.Fatalf("no-op %s wrote revision/audit: revisions %d→%d, audits %d→%d", body, revs, r2, audits, a2)
		}
	}
	noop(a.Id, a.Version, `{}`)
	noop(a.Id, a.Version, `{"displayName":"满字段资产"}`)
	noop(a.Id, a.Version, `{"displayName":"  满字段资产  ","brandSnapshot":" 品牌A "}`)
	noop(a.Id, a.Version, `{"manufactureDate":"2020-01-02","liftingHeightM":12.5,"manufacturerId":"mfr-p001"}`)
	noop(a.Id, a.Version, `{"assetStatus":"DRAFT"}`)
	noop(a.Id, a.Version, `{"id":"`+a.Id+`","projectId":"P001","version":`+itoa(a.Version)+`}`)
	noop(nullCode.Id, nullCode.Version, `{"assetCode":null}`)
	noop(nullCode.Id, nullCode.Version, `{"assetCode":"   ","serialNumber":null,"manufactureDate":null}`)

	// Validation and concurrency still run before the no-op decision.
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version+5), raw(`{}`)); w.Code != http.StatusConflict {
		t.Fatalf("stale no-op = %d, want 409", w.Code)
	}
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version), raw(`{"displayName":null}`)); w.Code != 422 {
		t.Fatalf("invalid no-op-shaped body = %d, want 422", w.Code)
	}

	// A real change after the no-ops: version +1, exactly one revision and one
	// audit, changed_fields lists only the field whose value changed (brandSnapshot
	// was re-sent unchanged and is not listed).
	revs, audits := p.counts(t, a.Id)
	w = p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version), raw(`{"brandSnapshot":"品牌A","modelSnapshot":"MG-60"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("real change = %d %s", w.Code, w.Body.String())
	}
	if got := p.get(t, a.Id); got.Version != a.Version+1 || got.ModelSnapshot != "MG-60" {
		t.Fatalf("real change stored v%d %q", got.Version, got.ModelSnapshot)
	}
	if r2, a2 := p.counts(t, a.Id); r2 != revs+1 || a2 != audits+1 {
		t.Fatalf("real change: revisions %d→%d, audits %d→%d (want +1 each)", revs, r2, audits, a2)
	}
	var changed string
	_ = p.f.db.QueryRow(`SELECT changed_fields::text FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1 ORDER BY revision DESC LIMIT 1`, a.Id).Scan(&changed)
	if changed != "{updated,modelSnapshot}" {
		t.Fatalf("changed_fields = %s, want {updated,modelSnapshot}", changed)
	}
}

// Two editors open v1. Editor A sends a no-op; it must not bump the version, so
// editor B's real edit on v1 still succeeds. Then A's stale real edit is 409.
func TestHTTPAssetPatchNoopDoesNotBreakOtherEditors(t *testing.T) {
	p := newPatchFixture(t)
	a := p.fullAsset(t, "CRN-NOOP-2")
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version), raw(`{"brandSnapshot":"品牌A"}`)); w.Code != 200 {
		t.Fatalf("editor A no-op = %d", w.Code)
	}
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version), raw(`{"brandSnapshot":"B的修改"}`)); w.Code != 200 {
		t.Fatalf("editor B on same version after A's no-op = %d, want 200", w.Code)
	}
	if w := p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+a.Id, ifMatch(a.Version), raw(`{"brandSnapshot":"A的旧草稿"}`)); w.Code != http.StatusConflict {
		t.Fatalf("editor A stale real edit = %d, want 409", w.Code)
	}
	if got := p.get(t, a.Id); got.BrandSnapshot != "B的修改" || got.Version != a.Version+1 {
		t.Fatalf("stored %q v%d", got.BrandSnapshot, got.Version)
	}
}

// The HTTP PATCH route must use the presence-aware PatchAsset, never the
// whole-object UpdateAsset wrapper (which would turn omitted keys into clears).
func TestHTTPAssetPatchRouteDoesNotUseWholeObjectUpdate(t *testing.T) {
	src, err := os.ReadFile("asset_api.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "asset.UpdateAsset(") {
		t.Fatal("asset_api.go must not call asset.UpdateAsset; use asset.PatchAsset")
	}
	if !strings.Contains(string(src), "asset.PatchAsset(") {
		t.Fatal("asset_api.go must call asset.PatchAsset for PATCH")
	}
}

// Create and PATCH normalize text the same way: an asset created with padded
// text, then PATCHed with the same padded values, is a no-op.
func TestHTTPAssetCreateAndPatchNormalizeAlike(t *testing.T) {
	p := newPatchFixture(t)
	w := p.f.doBody(p.cookie, p.csrf, "POST", "/api/v1/assets", nil, raw(`{"projectId":"P001","assetType":" CRANE ","displayName":" 名称 ","assetCode":" CRN-NORM-1 ","serialNumber":"  "}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Id      string
		Version int64
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	stored := p.get(t, created.Id)
	if stored.DisplayName != "名称" || stored.AssetType != "CRANE" || s(stored.AssetCode) != "CRN-NORM-1" || stored.SerialNumber != nil {
		t.Fatalf("create not normalized: %+v", stored)
	}
	w = p.f.doBody(p.cookie, p.csrf, "PATCH", "/api/v1/assets/"+created.Id, ifMatch(created.Version), raw(`{"assetType":" CRANE ","displayName":" 名称 ","assetCode":" CRN-NORM-1 ","serialNumber":"  "}`))
	if w.Code != 200 {
		t.Fatalf("same-value patch = %d", w.Code)
	}
	if got := p.get(t, created.Id); got.Version != created.Version {
		t.Fatalf("same padded values bumped version v%d → v%d", created.Version, got.Version)
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The equipment type dictionary requires a session and lists only types that are
// both in the code-owned registry and ACTIVE in equipment_types, with the
// registry's Chinese name (not the DB display_name).
func TestHTTPEquipmentTypesDictionary(t *testing.T) {
	p := newPatchFixture(t)
	if _, err := p.f.db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain,spec_schema_version,status) VALUES
	  ('MOTOR','Motor','MECHANICAL','1','ACTIVE'),('PLC','PLC','CONTROL','1','DISABLED'),('NOT_IN_REGISTRY','X','OTHER','1','ACTIVE')`); err != nil {
		t.Fatal(err)
	}
	if w := p.f.doBody(nil, "", "GET", "/api/v1/equipment/types", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", w.Code)
	}
	w := p.f.doBody(p.cookie, "", "GET", "/api/v1/equipment/types", nil, nil)
	if w.Code != 200 {
		t.Fatalf("types = %d", w.Code)
	}
	var types []struct{ TypeCode, DisplayName, Domain string }
	if err := json.Unmarshal(w.Body.Bytes(), &types); err != nil {
		t.Fatalf("decode: %v %s", err, w.Body.String())
	}
	if len(types) != 1 || types[0].TypeCode != "MOTOR" || types[0].DisplayName != "电机" || types[0].Domain != "MECHANICAL" {
		t.Fatalf("types = %s, want only MOTOR/电机 (PLC disabled, NOT_IN_REGISTRY unregistered)", w.Body.String())
	}
}
