package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/metering"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// R9 收口 G3 — real-asset readiness over the production BFF: platform_admin only writes, If-Match
// on every decision, history (entity_revisions / point_config_changes) and audit for each, a
// confirmed point settles its review issues with the change, an unconfirmed REAL point is never a
// trusted maintenance meter, and the 53 imported draft rules stay unapproved.
func TestRealAssetReadinessHTTP(t *testing.T) {
	f := lhkFixture(t)
	ctx := context.Background()
	viewer, vs := f.login(t, "a")
	maint, ms := f.login(t, "maintainer")
	admin, as := f.login(t, "admin")
	outsider, os := f.login(t, "b")
	var ready metering.Readiness
	get := func(c *http.Cookie, csrf, path string, out any) int {
		w := f.request(c, csrf, path, "GET")
		if out != nil && w.Code == 200 {
			_ = json.Unmarshal(w.Body.Bytes(), out)
		}
		return w.Code
	}
	if code := get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/readiness", &ready); code != 200 || ready.PointsTotal != 55 || ready.PointsConfirmed != 0 || ready.RulesDraft != 53 || ready.RulesApproved != 0 || ready.RealRuntime || len(ready.Blockers) < 6 {
		t.Fatalf("initial readiness %d %+v", code, ready)
	}
	if code := get(outsider, os.CsrfToken, "/api/v1/assets/LHK-CRANE-001/readiness", nil); code != 404 {
		t.Fatalf("outsider readiness %d", code)
	}
	post := func(c *http.Cookie, csrf, path string, version int64, body any) int {
		h := map[string]string{}
		if version > 0 {
			h["If-Match"] = `"` + itoa64(version) + `"`
		}
		return f.doBody(c, csrf, "POST", "/api/v1/assets/LHK-CRANE-001/"+path, h, body).Code
	}
	// --- nameplate: rated capacity confirmed → asset ledger + revision; maintainer 403; stale 409.
	var pending []metering.PendingView
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/pending-attributes", &pending)
	var rated metering.PendingView
	for _, p := range pending {
		if p.Subject == "asset" && p.Attribute == "ratedCapacityT" {
			rated = p
		}
	}
	body := map[string]any{"status": "CONFIRMED", "value": 10, "basis": "铭牌照片 IMG-001：额定起重量 10 t"}
	if code := post(maint, ms.CsrfToken, "pending-attributes/"+rated.ID+"/decision", rated.Version, body); code != 403 {
		t.Fatalf("maintainer decided nameplate: %d", code)
	}
	if code := post(admin, as.CsrfToken, "pending-attributes/"+rated.ID+"/decision", 0, body); code != 428 {
		t.Fatalf("missing If-Match: %d", code)
	}
	if code := post(admin, as.CsrfToken, "pending-attributes/"+rated.ID+"/decision", rated.Version+5, body); code != 409 {
		t.Fatalf("stale version: %d", code)
	}
	if code := post(admin, as.CsrfToken, "pending-attributes/"+rated.ID+"/decision", rated.Version, map[string]any{"status": "CONFIRMED", "value": -1, "basis": "x y"}); code != 422 {
		t.Fatalf("negative capacity: %d", code)
	}
	if code := post(admin, as.CsrfToken, "pending-attributes/"+rated.ID+"/decision", rated.Version, body); code != 204 {
		t.Fatalf("confirm rated capacity: %d", code)
	}
	if f.count(t, `SELECT count(*) FROM asset_devices WHERE id='LHK-CRANE-001' AND rated_capacity_t=10`) != 1 ||
		f.count(t, `SELECT count(*) FROM entity_revisions WHERE entity_type='asset_pending_attribute' AND entity_id=$1`, rated.ID) != 1 ||
		f.count(t, `SELECT count(*) FROM audit_logs WHERE action='pending_attribute_decided'`) != 1 {
		t.Fatal("nameplate decision not recorded")
	}
	// --- installation date of the slot's actual instance.
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/readiness", &ready)
	var slot metering.ReadinessSlot
	for _, s := range ready.Slots {
		if s.Slot == "GANTRY-01" {
			slot = s
		}
	}
	if code := post(admin, as.CsrfToken, "equipment-slots/GANTRY-01/installed-at", slot.Version, map[string]any{"installedAt": time.Now().Add(48 * time.Hour), "basis": "未来日期"}); code != 422 {
		t.Fatalf("future installation date: %d", code)
	}
	installed := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	if code := post(admin, as.CsrfToken, "equipment-slots/GANTRY-01/installed-at", slot.Version, map[string]any{"installedAt": installed, "basis": "竣工资料 第 3 页"}); code != 204 {
		t.Fatalf("installed-at: %d", code)
	}
	// --- point confirmation: needs all open issues settled; active level + edge for a counted bit.
	var points []metering.PointView
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/points", &points)
	var p1 metering.PointView
	for _, p := range points {
		if p.Code == "LHK-P001" {
			p1 = p
		}
	}
	var issues []metering.IssueView
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/point-issues", &issues)
	open := []string{}
	for _, i := range issues {
		if i.PointCode != nil && *i.PointCode == "LHK-P001" && i.Status == "OPEN" {
			open = append(open, i.ID)
		}
	}
	confirm := map[string]any{"status": "CONFIRMED", "activeLevel": "HIGH", "basis": "PLC 程序 OB1 网络 12 + 现场 I/O 点动", "resolveIssues": open}
	if code := post(admin, as.CsrfToken, "points/LHK-P001/confirmation", p1.Version, confirm); code != 422 {
		t.Fatalf("counted bit without edge: %d", code)
	}
	confirm["edge"] = "RISING"
	if len(open) > 0 {
		short := map[string]any{"status": "CONFIRMED", "activeLevel": "HIGH", "edge": "RISING", "basis": "x y", "resolveIssues": []string{}}
		if code := post(admin, as.CsrfToken, "points/LHK-P001/confirmation", p1.Version, short); code != 422 {
			t.Fatalf("open issues left: %d", code)
		}
	}
	if code := post(admin, as.CsrfToken, "points/LHK-P001/confirmation", p1.Version, confirm); code != 204 {
		t.Fatalf("confirm point: %d", code)
	}
	if code := post(admin, as.CsrfToken, "points/LHK-P001/confirmation", p1.Version, confirm); code != 409 {
		t.Fatalf("stale point version: %d", code)
	}
	if f.count(t, `SELECT count(*) FROM point_config_changes c JOIN telemetry_points p ON p.id=c.point_id WHERE p.point_code='LHK-P001' AND c.to_version=2`) != 1 ||
		f.count(t, `SELECT count(*) FROM point_review_issues i JOIN telemetry_points p ON p.id=i.point_id WHERE p.point_code='LHK-P001' AND i.status='OPEN'`) != 0 ||
		f.count(t, `SELECT count(*) FROM meter_definitions d JOIN telemetry_points p ON p.id=d.point_id WHERE p.point_code='LHK-P001' AND d.status='ACTIVE'`) < 1 {
		t.Fatal("point confirmation not applied with its history")
	}
	if len(open) > 0 && f.count(t, `SELECT count(*) FROM point_review_issues WHERE config_change_id IS NOT NULL`) != len(open) {
		t.Fatal("issues not linked to the configuration change")
	}
	var changes []metering.PointChange
	if code := get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/point-changes", &changes); code != 200 || len(changes) != 1 || changes[0].PointCode != "LHK-P001" {
		t.Fatalf("point changes %d %+v", code, changes)
	}
	// --- statutory inspection.
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/readiness", &ready)
	insp := map[string]any{"latestInspectionDate": "2026-03-01", "inspectionValidUntil": "2028-02-29", "useRegistrationNo": "起17鄂F0001(26)", "basis": "检验报告 NO.2026-0301"}
	if code := post(admin, as.CsrfToken, "statutory-inspection", ready.AssetVersion, map[string]any{"latestInspectionDate": "2026-03-01", "inspectionValidUntil": "2025-01-01", "basis": "x y"}); code != 422 {
		t.Fatalf("inverted inspection dates: %d", code)
	}
	if code := post(admin, as.CsrfToken, "statutory-inspection", ready.AssetVersion, insp); code != 204 {
		t.Fatalf("statutory inspection: %d", code)
	}
	if f.count(t, `SELECT count(*) FROM asset_devices WHERE id='LHK-CRANE-001' AND inspection_valid_until='2028-02-29'`) != 1 ||
		f.count(t, `SELECT count(*) FROM asset_pending_attributes WHERE attribute='statutoryInspection' AND status='CONFIRMED'`) != 1 {
		t.Fatal("inspection not recorded")
	}
	// --- REAL baseline needs the site value and basis (unknown = not recorded).
	if code := f.doBody(admin, as.CsrfToken, "POST", "/api/v1/assets/LHK-CRANE-001/meter-baselines", nil, map[string]any{"equipmentSlot": "GANTRY-01", "meterCode": "LHK-P001.cycles", "initialUsage": nil, "note": ""}).Code; code != 422 {
		t.Fatalf("REAL baseline without value: %d", code)
	}
	if code := f.doBody(admin, as.CsrfToken, "POST", "/api/v1/assets/LHK-CRANE-001/meter-baselines", nil, map[string]any{"equipmentSlot": "GANTRY-01", "meterCode": "LHK-P001.cycles", "initialUsage": 120000, "note": "接触器计数器读数 2026-11-19"}).Code; code != 204 {
		t.Fatalf("REAL baseline: %d", code)
	}
	// --- an approved REAL rule on an UNCONFIRMED point is never evaluated as trusted; on the
	// confirmed point it is (with a REAL runtime in this isolated test only).
	var rule metering.RuleView
	var rules []metering.RuleView
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/maintenance-rules", &rules)
	for _, r := range rules {
		if r.Code == "LIFE-GANTRY-02" {
			rule = r
		}
	}
	approve := map[string]any{"intervalMeter": 1000000, "effectiveFrom": time.Now().Add(-time.Hour).Format(time.RFC3339), "basis": "厂家手册 第 12 页"}
	if code := post(maint, ms.CsrfToken, "maintenance-rules/"+rule.ID+"/approve", int64(rule.Version), approve); code != 403 {
		t.Fatalf("maintainer approved: %d", code)
	}
	if code := post(admin, as.CsrfToken, "maintenance-rules/"+rule.ID+"/approve", int64(rule.Version)+1, approve); code != 409 {
		t.Fatalf("stale rule version approved: %d", code)
	}
	if code := post(admin, as.CsrfToken, "maintenance-rules/"+rule.ID+"/approve", int64(rule.Version), approve); code != 204 {
		t.Fatalf("approve: %d", code)
	}
	if _, err := f.db.Exec(`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES('REAL-LHK-ISOLATED','LHK','LHK-CRANE-001','REAL')`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	send := func(seq int64, point string, val float64) {
		m := contract.WithPayload(contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: "demo", ProjectId: "LHK", DeviceId: "REAL-LHK-ISOLATED", BootId: "real", BootSeq: 1, Sequence: seq, SentAt: now, Kind: "METER"},
			contract.MeterBatchPayload{SourceMode: "REAL", SampledAt: now, Samples: []contract.MeterSample{{PointCode: point, Metric: "cycles", Value: &val, Quality: "GOOD"}}, Faults: []contract.FaultSample{}})
		if _, err := metering.Ingest(ctx, f.db, m); err != nil {
			t.Fatal(err)
		}
	}
	send(1, "LHK-P002", 10)
	send(2, "LHK-P002", 2000000)
	if _, err := metering.EvaluateAll(ctx, f.db, "LHK-CRANE-001", now); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := f.db.QueryRow(`SELECT s.status, COALESCE(s.reason,'') FROM maintenance_schedules s WHERE s.rule_id=$1 AND s.source_mode='REAL'`, rule.ID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != metering.StatusInsufficient || f.count(t, `SELECT count(*) FROM maintenance_alerts WHERE source_mode='REAL'`) != 0 {
		t.Fatalf("unconfirmed REAL point used as a trusted meter: %s %q", status, reason)
	}
	// Drafts: exactly one approved, 52 remain unapproved.
	if f.count(t, `SELECT count(*) FROM maintenance_rules WHERE asset_id='LHK-CRANE-001' AND NOT test_only AND approval_status='DRAFT_UNAPPROVED'`) != 52 {
		t.Fatal("draft rules changed")
	}
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/readiness", &ready)
	if ready.PointsConfirmed != 1 || ready.SlotsWithDate != 1 || ready.RealBaselines != 1 || ready.RulesApproved != 1 || ready.InspectionValidUntil == nil || ready.RatedCapacityT == nil || *ready.RatedCapacityT != 10 {
		t.Fatalf("readiness after decisions %+v", ready)
	}
}
