package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/metering"
	"github.com/guolichen007/crane-operations-platform/internal/onboarding"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// lhkFixture: the production BFF with the imported first asset; user-maintainer / user-admin /
// user-a are granted on LHK as maintainer / platform_admin / viewer; user-b has no LHK grant.
func lhkFixture(t *testing.T) securityFixture {
	t.Helper()
	f := newSecurityFixture(t)
	ctx := context.Background()
	scope, _ := equipmenttypes.Scope(true, nil)
	tp, err := equipmenttypes.PlanTypes(ctx, f.db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = equipmenttypes.Apply(ctx, f.db, scope, tp.Digest, "ops", "types"); err != nil {
		t.Fatal(err)
	}
	b, _ := onboarding.Embedded()
	p, _, err := onboarding.PlanImport(ctx, f.db, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = onboarding.Apply(ctx, f.db, b, p.Digest, "ops", "import"); err != nil {
		t.Fatal(err)
	}
	for user, role := range map[string]string{"maintainer": "maintainer", "admin": "platform_admin", "a": "viewer"} {
		if err = identity.ChangeGrant(ctx, f.db, "user-"+user, "LHK", role, false, auditlog.Event{Actor: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.db.Exec(`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES('SIM-LHK-CRANE-001','LHK','LHK-CRANE-001','SIMULATION')`); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMaintenanceHTTPScopeRolesAndWorkOrderFlow(t *testing.T) {
	f := lhkFixture(t)
	ctx := context.Background()
	viewer, vs := f.login(t, "a")
	outsider, os := f.login(t, "b")
	maint, ms := f.login(t, "maintainer")
	admin, as := f.login(t, "admin")
	get := func(c *http.Cookie, csrf, path string, out any) int {
		w := f.request(c, csrf, path, "GET")
		if out != nil && w.Code == 200 {
			_ = json.Unmarshal(w.Body.Bytes(), out)
		}
		return w.Code
	}
	// Reads: viewer 200 on every tab; an outsider never sees the asset (404); anonymous 401.
	for _, tab := range []string{"points", "point-issues", "pending-attributes", "meters", "maintenance-rules", "maintenance-alerts", "work-orders", "maintenance-history"} {
		if code := get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/"+tab, nil); code != 200 {
			t.Fatalf("viewer %s = %d", tab, code)
		}
		if code := get(outsider, os.CsrfToken, "/api/v1/assets/LHK-CRANE-001/"+tab, nil); code != 404 {
			t.Fatalf("outsider %s = %d", tab, code)
		}
		if w := f.request(nil, "", "/api/v1/assets/LHK-CRANE-001/"+tab, "GET"); w.Code != 401 {
			t.Fatalf("anonymous %s = %d", tab, w.Code)
		}
	}
	var points []metering.PointView
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/points", &points)
	if len(points) != 55 || points[0].Code != "LHK-P001" || points[0].PLCAddressRaw == nil || *points[0].PLCAddressRaw != "DB505.DBX32.0" || points[0].ConfirmationStatus != "UNCONFIRMED" {
		t.Fatalf("points %d %+v", len(points), points[0])
	}
	// Simulated data through the real ingest path, then evaluate a test-only fixture rule.
	now := time.Now().UTC()
	send := func(seq int64, val float64) {
		m := contract.WithPayload(contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: "demo", ProjectId: "LHK", DeviceId: "SIM-LHK-CRANE-001", BootId: "b", BootSeq: 1, Sequence: seq, SentAt: now, Kind: "METER"},
			contract.MeterBatchPayload{SourceMode: "SIMULATION", SampledAt: now, Samples: []contract.MeterSample{{PointCode: "LHK-P001", Metric: "cycles", Value: &val, Quality: "GOOD"}}, Faults: []contract.FaultSample{}})
		if _, err := metering.Ingest(ctx, f.db, m); err != nil {
			t.Fatal(err)
		}
	}
	send(1, 0)
	if err := metering.RecordBaseline(ctx, f.db, "LHK-CRANE-001", "GANTRY-01", "LHK-P001.cycles", "SIMULATION", nil, "test", ""); err != nil {
		t.Fatal(err)
	}
	interval := 100.0
	if _, err := metering.UpsertTestRule(ctx, f.db, "LHK-CRANE-001", metering.TestRule{Code: "T-HTTP", Name: "HTTP 测试规则", Kind: "REPLACEMENT", Type: "METER_BASED", Slot: "GANTRY-01", MeterCode: "LHK-P001.cycles", IntervalMeter: &interval}, "test"); err != nil {
		t.Fatal(err)
	}
	send(2, 100)
	if _, err := metering.EvaluateAll(ctx, f.db, "LHK-CRANE-001", now); err != nil {
		t.Fatal(err)
	}
	var alerts []metering.AlertView
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/maintenance-alerts", &alerts)
	var due string
	for _, a := range alerts {
		if a.Stage == "PCT_100" && a.SourceMode == "SIMULATION" {
			due = a.ID
		}
	}
	if due == "" {
		t.Fatalf("no PCT_100 alert: %+v", alerts)
	}
	// Viewer cannot open a work order (403); outsider 404; maintainer creates it (201).
	if w := f.doBody(viewer, vs.CsrfToken, "POST", "/api/v1/maintenance-alerts/"+due+"/work-order", nil, nil); w.Code != 403 {
		t.Fatalf("viewer created a work order: %d", w.Code)
	}
	if w := f.doBody(outsider, os.CsrfToken, "POST", "/api/v1/maintenance-alerts/"+due+"/work-order", nil, nil); w.Code != 404 {
		t.Fatalf("outsider: %d", w.Code)
	}
	w := f.doBody(maint, ms.CsrfToken, "POST", "/api/v1/maintenance-alerts/"+due+"/work-order", nil, nil)
	var wo metering.WorkOrder
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &wo) != nil || wo.SourceMode != "SIMULATION" {
		t.Fatalf("create work order %d %s", w.Code, w.Body.String())
	}
	// Transitions need If-Match and CSRF.
	if w = f.doBody(maint, ms.CsrfToken, "POST", "/api/v1/work-orders/"+wo.ID+"/transitions", nil, map[string]any{"to": "ASSIGNED", "assignedTo": "tech"}); w.Code != 428 {
		t.Fatalf("missing If-Match: %d", w.Code)
	}
	if w = f.doBody(maint, "", "POST", "/api/v1/work-orders/"+wo.ID+"/transitions", map[string]string{"If-Match": `"1"`}, map[string]any{"to": "ASSIGNED", "assignedTo": "tech"}); w.Code != 403 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	step := func(c *http.Cookie, csrf string, body map[string]any) int {
		w := f.doBody(c, csrf, "POST", "/api/v1/work-orders/"+wo.ID+"/transitions", map[string]string{"If-Match": `"` + itoa64(wo.Version) + `"`}, body)
		if w.Code == 200 {
			_ = json.Unmarshal(w.Body.Bytes(), &wo)
		}
		return w.Code
	}
	if code := step(maint, ms.CsrfToken, map[string]any{"to": "IN_PROGRESS"}); code != 422 {
		t.Fatalf("OPEN→IN_PROGRESS: %d", code)
	}
	if code := step(maint, ms.CsrfToken, map[string]any{"to": "ASSIGNED", "assignedTo": "tech-1"}); code != 200 {
		t.Fatalf("assign: %d", code)
	}
	if code := step(maint, ms.CsrfToken, map[string]any{"to": "IN_PROGRESS"}); code != 200 {
		t.Fatalf("start: %d", code)
	}
	if code := step(maint, ms.CsrfToken, map[string]any{"to": "COMPLETED", "result": "DONE", "basis": "模拟更换", "replacement": map[string]any{"reason": "寿命到期（模拟）", "serialNumber": "S2"}}); code != 200 {
		t.Fatalf("complete with SIM replacement: %d", code)
	}
	if f.count(t, `SELECT count(*) FROM equipment_replacements WHERE source_mode='SIMULATION' AND new_equipment_installation_id IS NULL`) != 1 {
		t.Fatal("simulated replacement not recorded")
	}
	// Rule approval is platform_admin only and refused for test-only rules.
	var draft string
	_ = f.db.QueryRow(`SELECT id FROM maintenance_rules WHERE rule_code='LIFE-GANTRY-03'`).Scan(&draft)
	body := map[string]any{"intervalDays": 3650, "effectiveFrom": now.Format(time.RFC3339), "basis": "厂家手册 10 年"}
	if w = f.doBody(maint, ms.CsrfToken, "POST", "/api/v1/assets/LHK-CRANE-001/maintenance-rules/"+draft+"/approve", nil, body); w.Code != 403 {
		t.Fatalf("maintainer approved a rule: %d", w.Code)
	}
	if w = f.doBody(admin, as.CsrfToken, "POST", "/api/v1/assets/LHK-CRANE-001/maintenance-rules/"+draft+"/approve", nil, body); w.Code != 204 {
		t.Fatalf("admin approve: %d %s", w.Code, w.Body.String())
	}
	var testRule string
	_ = f.db.QueryRow(`SELECT id FROM maintenance_rules WHERE rule_code='T-HTTP'`).Scan(&testRule)
	if w = f.doBody(admin, as.CsrfToken, "POST", "/api/v1/assets/LHK-CRANE-001/maintenance-rules/"+testRule+"/approve", nil, body); w.Code != 409 {
		t.Fatalf("test-only rule approved: %d", w.Code)
	}
	// Center: REAL attention excludes simulation; the approved real rule without installation date
	// is INSUFFICIENT_DATA; statutory inspection unknown is counted.
	if _, err := metering.EvaluateAll(ctx, f.db, "LHK-CRANE-001", now); err != nil {
		t.Fatal(err)
	}
	var center struct {
		Rows      []metering.CenterRow     `json:"rows"`
		Attention metering.AttentionCounts `json:"attention"`
	}
	if code := get(viewer, vs.CsrfToken, "/api/v1/maintenance/center", &center); code != 200 {
		t.Fatal("center", code)
	}
	if center.Attention.InsufficientData != 1 || center.Attention.MaintenanceDue != 0 || center.Attention.OpenWorkOrders != 0 || center.Attention.InspectionUnknown < 1 {
		t.Fatalf("attention %+v", center.Attention)
	}
	var outsiderCenter struct {
		Rows []metering.CenterRow `json:"rows"`
	}
	get(outsider, os.CsrfToken, "/api/v1/maintenance/center", &outsiderCenter)
	for _, r := range outsiderCenter.Rows {
		if r.AssetID == "LHK-CRANE-001" {
			t.Fatal("center leaked an unauthorized asset")
		}
	}
	var history []metering.HistoryEntry
	get(viewer, vs.CsrfToken, "/api/v1/assets/LHK-CRANE-001/maintenance-history", &history)
	cats := map[string]bool{}
	for _, h := range history {
		cats[h.Category] = true
	}
	if !cats["WORK_ORDER"] || !cats["REPLACEMENT"] || !cats["LEDGER"] {
		t.Fatalf("history categories %v", cats)
	}
}
