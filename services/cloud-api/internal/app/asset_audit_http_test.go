package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

// doBody is securityFixture.request with an optional JSON body and extra headers,
// mirroring the production origin/CSRF requirements for state-changing methods.
func (f securityFixture) doBody(cookie *http.Cookie, csrf, method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		r = httptest.NewRequest(method, f.server.URL+path, strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, f.server.URL+path, nil)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if stateChanging(method) {
		r.Header.Set("Origin", f.server.URL)
		r.Header.Set("X-CSRF-Token", csrf)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

// A real HTTP business write must correlate the response X-Request-Id with
// entity_revisions and audit_logs, and failed writes must not fabricate success
// records. This exercises the full middleware stack (session, CSRF, request-id)
// rather than calling the Go domain functions directly.
func TestAssetHTTPAuditCorrelation(t *testing.T) {
	f := newSecurityFixture(t)
	admin, adminInfo := f.login(t, "admin")
	viewer, viewerInfo := f.login(t, "a")

	// First successful write: X-Request-Id ↔ entity_revisions ↔ audit_logs.
	create := f.doBody(admin, adminInfo.CsrfToken, "POST", "/api/v1/assets",
		map[string]string{"X-Request-Id": "req-http-1"},
		map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-H1"})
	if create.Code != http.StatusCreated {
		t.Fatalf("asset create = %d, want 201 (%s)", create.Code, create.Body.String())
	}
	if rid := create.Header().Get("X-Request-Id"); rid != "req-http-1" {
		t.Fatalf("response X-Request-Id = %q, want req-http-1", rid)
	}
	var created contract.AssetMasterData
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var revReq, revActor, revProject string
	if err := f.db.QueryRow(`SELECT request_id,changed_by,project_id FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1`, created.Id).Scan(&revReq, &revActor, &revProject); err != nil {
		t.Fatal(err)
	}
	var audReq, audActor, audProject string
	if err := f.db.QueryRow(`SELECT request_id,user_id,project_id FROM audit_logs WHERE action='asset_created' AND project_id='P001'`).Scan(&audReq, &audActor, &audProject); err != nil {
		t.Fatal(err)
	}
	if revReq != "req-http-1" || audReq != "req-http-1" || revReq != audReq {
		t.Fatalf("request id mismatch: revision=%q audit=%q, want req-http-1", revReq, audReq)
	}
	if revActor != "user-admin" || audActor != "user-admin" || revProject != "P001" || audProject != "P001" {
		t.Fatalf("actor/project mismatch: revision=%q/%q audit=%q/%q", revActor, revProject, audActor, audProject)
	}

	// Version conflict: no success audit and no new revision for the attempt.
	conflict := f.doBody(admin, adminInfo.CsrfToken, "PATCH", "/api/v1/assets/"+created.Id,
		map[string]string{"X-Request-Id": "req-conflict-1", "If-Match": `"999"`},
		map[string]any{"assetCode": "CRN-H2"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict = %d, want 409 (%s)", conflict.Code, conflict.Body.String())
	}
	var conflictAudits, conflictRevisions int
	if err := f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE request_id='req-conflict-1' AND result='success'`).Scan(&conflictAudits); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE request_id='req-conflict-1'`).Scan(&conflictRevisions); err != nil {
		t.Fatal(err)
	}
	if conflictAudits != 0 || conflictRevisions != 0 {
		t.Fatalf("conflict produced %d success audits / %d revisions, want 0/0", conflictAudits, conflictRevisions)
	}

	// Permission denial (viewer): no business revision, no success business audit.
	deny := f.doBody(viewer, viewerInfo.CsrfToken, "POST", "/api/v1/assets",
		map[string]string{"X-Request-Id": "req-deny-1"},
		map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-DENY"})
	if deny.Code != http.StatusForbidden {
		t.Fatalf("viewer write = %d, want 403 (%s)", deny.Code, deny.Body.String())
	}
	var denyRevisions, denyCreated int
	if err := f.db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE request_id='req-deny-1'`).Scan(&denyRevisions); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE request_id='req-deny-1' AND action='asset_created'`).Scan(&denyCreated); err != nil {
		t.Fatal(err)
	}
	if denyRevisions != 0 || denyCreated != 0 {
		t.Fatalf("denial produced %d revisions / %d asset_created audits, want 0/0", denyRevisions, denyCreated)
	}

	// Idempotent replay carries a new HTTP request id but must not fabricate a
	// second success revision/audit under that new id.
	first := f.doBody(admin, adminInfo.CsrfToken, "POST", "/api/v1/assets",
		map[string]string{"X-Request-Id": "req-idem-1", "Idempotency-Key": "idem-h1"},
		map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-IDEM"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first idempotent create = %d (%s)", first.Code, first.Body.String())
	}
	var firstOut contract.AssetMasterData
	if err := json.Unmarshal(first.Body.Bytes(), &firstOut); err != nil {
		t.Fatal(err)
	}
	replay := f.doBody(admin, adminInfo.CsrfToken, "POST", "/api/v1/assets",
		map[string]string{"X-Request-Id": "req-idem-2", "Idempotency-Key": "idem-h1"},
		map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-IDEM"})
	if replay.Code != http.StatusCreated {
		t.Fatalf("idempotent replay = %d (%s)", replay.Code, replay.Body.String())
	}
	var replayOut contract.AssetMasterData
	if err := json.Unmarshal(replay.Body.Bytes(), &replayOut); err != nil {
		t.Fatal(err)
	}
	if replayOut.Id != firstOut.Id {
		t.Fatalf("replay returned different id: %s vs %s", replayOut.Id, firstOut.Id)
	}
	var replayAudits, replayRevisions int
	if err := f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE request_id='req-idem-2' AND result='success'`).Scan(&replayAudits); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE request_id='req-idem-2'`).Scan(&replayRevisions); err != nil {
		t.Fatal(err)
	}
	if replayAudits != 0 || replayRevisions != 0 {
		t.Fatalf("replay fabricated %d audits / %d revisions under new request id, want 0/0", replayAudits, replayRevisions)
	}
}

// Equipment install over HTTP must also correlate the request id and record the
// validated spec in the created revision snapshot.
func TestEquipmentHTTPAuditCorrelation(t *testing.T) {
	f := newSecurityFixture(t)
	admin, adminInfo := f.login(t, "admin")
	if _, err := f.db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain,spec_schema_version,status) VALUES('MOTOR','MOTOR','MECHANICAL','1','ACTIVE')`); err != nil {
		t.Fatal(err)
	}
	create := f.doBody(admin, adminInfo.CsrfToken, "POST", "/api/v1/assets",
		map[string]string{"X-Request-Id": "req-eq-asset"},
		map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-EQ1"})
	if create.Code != http.StatusCreated {
		t.Fatalf("asset create = %d (%s)", create.Code, create.Body.String())
	}
	var assetOut contract.AssetMasterData
	if err := json.Unmarshal(create.Body.Bytes(), &assetOut); err != nil {
		t.Fatal(err)
	}

	install := f.doBody(admin, adminInfo.CsrfToken, "POST", "/api/v1/assets/"+assetOut.Id+"/equipment",
		map[string]string{"X-Request-Id": "req-eq-1"},
		map[string]any{
			"assetId":       assetOut.Id,
			"equipmentCode": "EQ-H1",
			"typeCode":      "MOTOR",
			"spec":          map[string]any{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true},
		})
	if install.Code != http.StatusCreated {
		t.Fatalf("equipment install = %d (%s)", install.Code, install.Body.String())
	}
	if rid := install.Header().Get("X-Request-Id"); rid != "req-eq-1" {
		t.Fatalf("response X-Request-Id = %q, want req-eq-1", rid)
	}
	var eq contract.EquipmentInstallation
	if err := json.Unmarshal(install.Body.Bytes(), &eq); err != nil {
		t.Fatal(err)
	}
	var revReq, audReq string
	if err := f.db.QueryRow(`SELECT request_id FROM entity_revisions WHERE entity_type='equipment' AND entity_id=$1`, eq.Id).Scan(&revReq); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT request_id FROM audit_logs WHERE action='equipment_created' AND project_id='P001'`).Scan(&audReq); err != nil {
		t.Fatal(err)
	}
	if revReq != "req-eq-1" || audReq != "req-eq-1" {
		t.Fatalf("equipment request id mismatch: revision=%q audit=%q, want req-eq-1", revReq, audReq)
	}
	var snapshot string
	if err := f.db.QueryRow(`SELECT snapshot_json::text FROM entity_revisions WHERE entity_type='equipment' AND entity_id=$1 ORDER BY revision ASC LIMIT 1`, eq.Id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, `"rated_power_kw"`) {
		t.Fatalf("equipment revision snapshot missing spec: %s", snapshot)
	}
}
