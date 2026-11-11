package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/geo"
)

const regBody = `{"lifecycle":"PLANNED","adminCode":330481,"locationLabel":"","scheduleText":"2027 Q1 安装","notes":"Q3"}`

// q3Fixture: Q2 fixture + user-new holds platform_admin on FP-A (via the real grant API),
// user-other holds viewer on FP-A; FP-B stays ungranted.
func q3Fixture(t *testing.T) (securityFixture, map[string]q3Session) {
	t.Helper()
	f := q2Fixture(t)
	s := map[string]q3Session{}
	for _, u := range []string{"pm", "a", "admin", "pmb", "new", "other"} {
		c, sess := f.login(t, u)
		s[u] = q3Session{c, sess.CsrfToken}
	}
	pm := s["pm"]
	for _, g := range []string{grantBody("user-new", rp("platform_admin"), nil), grantBody("user-other", rp("viewer"), nil)} {
		if w := f.requestJSON(pm.c, pm.csrf, "/api/v1/portfolio/projects/FP-A/members", "POST", g); w.Code != 200 {
			t.Fatalf("fixture grant: %d %s", w.Code, w.Body.String())
		}
	}
	return f, s
}

type q3Session struct {
	c    *http.Cookie
	csrf string
}

func TestInventoryRegistrationPermissionsHTTP(t *testing.T) {
	f, s := q3Fixture(t)
	path := "/api/v1/projects/FP-A/inventory-registration"
	before := f.count(t, `SELECT count(*) FROM projects WHERE lifecycle IS NOT NULL`)
	for _, c := range []struct {
		name, who, path string
		want            int
	}{
		// Portfolio rights never substitute for a ProjectGrant (manager approved FP-A but holds no grant).
		{"portfolio manager without grant", "pm", path, 404},
		{"portfolio planner", "a", path, 404},
		{"project-only platform_admin of another project", "admin", path, 404},
		{"other scope manager", "pmb", path, 404},
		// A grant without location_write.
		{"viewer on FP-A", "other", path, 403},
		// Granted user on an ungranted / unapproved / Inventory project.
		{"granted user on FP-B", "new", "/api/v1/projects/FP-B/inventory-registration", 404},
		{"unapproved project", "new", "/api/v1/projects/FP-UNAPPROVED/inventory-registration", 404},
	} {
		if w := f.requestJSON(s[c.who].c, s[c.who].csrf, c.path, "POST", regBody); w.Code != c.want {
			t.Errorf("%s: want %d, got %d %s", c.name, c.want, w.Code, w.Body.String())
		}
	}
	// A legacy Inventory project the caller may write (P001 for user-admin) is not a formal project.
	if w := f.requestJSON(s["admin"].c, s["admin"].csrf, "/api/v1/projects/P001/inventory-registration", "POST", regBody); w.Code != 404 {
		t.Errorf("Inventory project P001 re-registration: %d %s", w.Code, w.Body.String())
	}
	// Invalid bodies (granted user) are 422 and write nothing.
	for name, body := range map[string]string{
		"unknown code":        strings.Replace(regBody, "330481", "999998", 1),
		"country code":        strings.Replace(regBody, "330481", "100000", 1),
		"bad lifecycle":       strings.Replace(regBody, "PLANNED", "LIVE", 1),
		"province in body":    strings.Replace(regBody, `"notes":"Q3"`, `"notes":"Q3","province":"广东省"`, 1),
		"coordinates in body": strings.Replace(regBody, `"notes":"Q3"`, `"notes":"Q3","longitude":120.1`, 1),
		"label too long":      strings.Replace(regBody, `"locationLabel":""`, `"locationLabel":"`+strings.Repeat("长", 241)+`"`, 1),
	} {
		if w := f.requestJSON(s["new"].c, s["new"].csrf, path, "POST", body); w.Code != 422 {
			t.Errorf("%s: want 422, got %d %s", name, w.Code, w.Body.String())
		}
	}
	if after := f.count(t, `SELECT count(*) FROM projects WHERE lifecycle IS NOT NULL`); after != before {
		t.Fatalf("refused registrations changed Inventory: %d -> %d", before, after)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_inventory_registered'`); n != 0 {
		t.Fatalf("refused registrations audited as success: %d", n)
	}
	// Administrative index unavailable → 503, nothing written, no invented location.
	f.api.geo = func() (*geo.Index, error) { return nil, errors.New("index unavailable") }
	if w := f.requestJSON(s["new"].c, s["new"].csrf, path, "POST", regBody); w.Code != 503 {
		t.Fatalf("geo failure: %d %s", w.Code, w.Body.String())
	}
	f.api.geo = nil
	if f.count(t, `SELECT count(*) FROM projects WHERE id='FP-A' AND lifecycle IS NULL`) != 1 {
		t.Fatal("geo failure wrote the project")
	}
}

func TestInventoryRegistrationFlowAndConsistencyHTTP(t *testing.T) {
	f, s := q3Fixture(t)
	nw := s["new"]
	// Before: FP-A is not in Inventory, but the granted user already registered assets in it.
	for i, code := range []string{"Q3-CRN-1", "Q3-CRN-2"} {
		if w := f.requestJSON(nw.c, nw.csrf, "/api/v1/assets", "POST", `{"projectId":"FP-A","assetType":"CRANE","assetCode":"`+code+`","displayName":"Q3 行车 `+string(rune('A'+i))+`"}`); w.Code != 201 {
			t.Fatalf("asset %s: %d %s", code, w.Code, w.Body.String())
		}
	}
	if w := f.request(nw.c, nw.csrf, "/api/v1/projects", "GET"); strings.Contains(w.Body.String(), `"FP-A"`) {
		t.Fatalf("FP-A in Inventory before registration: %s", w.Body.String())
	}
	if w := f.request(nw.c, nw.csrf, "/api/v1/inventory/pending-projects", "GET"); !strings.Contains(w.Body.String(), `"id":"FP-A"`) || !strings.Contains(w.Body.String(), `"canRegister":true`) || !strings.Contains(w.Body.String(), `"assetCount":2`) {
		t.Fatalf("pending list: %s", w.Body.String())
	}
	// Viewer sees it as pending but cannot register; others see nothing.
	if w := f.request(s["other"].c, s["other"].csrf, "/api/v1/inventory/pending-projects", "GET"); !strings.Contains(w.Body.String(), `"canRegister":false`) {
		t.Fatalf("viewer pending: %s", w.Body.String())
	}
	if w := f.request(s["pm"].c, s["pm"].csrf, "/api/v1/inventory/pending-projects", "GET"); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("manager without grant sees pending projects: %s", w.Body.String())
	}

	w := f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", regBody)
	if w.Code != 200 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	var p struct {
		Id, Name, Lifecycle, Province, City, County, LocationLabel               string
		AdminCode                                                                int
		AssetDeviceCount, RuntimeDeviceCount, RealConnectedCount, SimulatedCount int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	// Names derived from the index (not typed); same id; counts are facts, not inferred.
	if p.Id != "FP-A" || p.Province != "浙江省" || p.City != "嘉兴市" || p.County != "海宁市" || p.AdminCode != 330481 || p.LocationLabel != "浙江省 嘉兴市 海宁市" {
		t.Fatalf("registered project: %s", w.Body.String())
	}
	if p.AssetDeviceCount != 2 || p.RuntimeDeviceCount != 0 || p.RealConnectedCount != 0 || p.SimulatedCount != 0 {
		t.Fatalf("counts invented: %s", w.Body.String())
	}
	// After: same id in /projects for granted users only; Inventory assets = asset ledger.
	if w := f.request(nw.c, nw.csrf, "/api/v1/projects", "GET"); !strings.Contains(w.Body.String(), `"id":"FP-A"`) {
		t.Fatalf("FP-A missing from Inventory: %s", w.Body.String())
	}
	if w := f.request(s["admin"].c, s["admin"].csrf, "/api/v1/projects", "GET"); strings.Contains(w.Body.String(), "FP-A") {
		t.Fatalf("ungranted platform_admin sees FP-A: %s", w.Body.String())
	}
	if w := f.request(s["pm"].c, s["pm"].csrf, "/api/v1/projects", "GET"); strings.Contains(w.Body.String(), "FP-A") {
		t.Fatalf("portfolio manager sees FP-A in Inventory: %s", w.Body.String())
	}
	w = f.request(nw.c, nw.csrf, "/api/v1/assets/devices", "GET")
	if strings.Count(w.Body.String(), `"projectId":"FP-A"`) != 2 {
		t.Fatalf("Inventory assets for FP-A: %s", w.Body.String())
	}
	w = f.request(nw.c, nw.csrf, "/api/v1/assets?project=FP-A", "GET")
	if !strings.Contains(w.Body.String(), "Q3-CRN-1") || !strings.Contains(w.Body.String(), "Q3-CRN-2") {
		t.Fatalf("asset ledger: %s", w.Body.String())
	}
	// Operations summary at the county: registered 2, REAL/SIM/TEST all 0, no runtime for both.
	w = f.request(nw.c, nw.csrf, "/api/v1/operations/summary?region=330481&alarmWindow=PT24H", "GET")
	var sum struct {
		Metrics struct {
			Assets struct{ Registered, NoRuntime int }
			Bound  struct{ REAL, SIMULATION, TEST int }
			Real   struct{ Connected int }
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &sum)
	if w.Code != 200 || sum.Metrics.Assets.Registered != 2 || sum.Metrics.Assets.NoRuntime != 2 || sum.Metrics.Real.Connected != 0 || sum.Metrics.Bound.REAL+sum.Metrics.Bound.SIMULATION+sum.Metrics.Bound.TEST != 0 {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}
	// Repeat → 409, nothing overwritten; audit exactly once with actor/project/request id.
	if w := f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", strings.Replace(regBody, "330481", "440300", 1)); w.Code != 409 {
		t.Fatalf("second registration: %d", w.Code)
	}
	if f.count(t, `SELECT count(*) FROM projects WHERE id='FP-A' AND admin_code=330481 AND lifecycle='PLANNED'`) != 1 {
		t.Fatal("second registration overwrote the project")
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_inventory_registered' AND project_id='FP-A' AND user_id='user-new' AND result='success:PLANNED:330481' AND request_id<>''`); n != 1 {
		t.Fatalf("registration audits = %d", n)
	}
	// Registration does not grant anyone anything.
	if n := f.count(t, `SELECT count(*) FROM project_memberships WHERE project_id='FP-A'`); n != 2 {
		t.Fatalf("memberships changed by registration: %d", n)
	}
}

func TestInventoryRegistrationConcurrencyHTTP(t *testing.T) {
	f, s := q3Fixture(t)
	nw := s["new"]
	codes := []string{"330481", "440300", "110105", "330100", "330481", "330400"}
	got := make([]int, len(codes))
	var wg sync.WaitGroup
	for i, c := range codes {
		wg.Add(1)
		go func(i int, c string) {
			defer wg.Done()
			got[i] = f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", strings.Replace(regBody, "330481", c, 1)).Code
		}(i, c)
	}
	wg.Wait()
	ok := 0
	for _, c := range got {
		if c == 200 {
			ok++
		} else if c != 409 {
			t.Fatalf("unexpected %d in %v", c, got)
		}
	}
	if ok != 1 {
		t.Fatalf("concurrent registrations: %v", got)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_inventory_registered' AND project_id='FP-A'`); n != 1 {
		t.Fatalf("audits = %d", n)
	}
}

// Q2 fix A: a committed grant whose member re-read fails is still reported as APPLIED (200),
// and a client retry of the same change gets 409 (already applied), never a second write.
func TestPortfolioGrantCommittedButReadFailsHTTP(t *testing.T) {
	f := q2Fixture(t)
	pm, spm := f.login(t, "pm")
	f.api.membersReadHook = func() error { return errors.New("injected read failure") }
	w := f.requestJSON(pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", "POST", grantBody("user-new", rp("viewer"), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"result":"APPLIED"`) || !strings.Contains(w.Body.String(), `"membersRefreshed":false`) {
		t.Fatalf("committed grant reported as: %d %s", w.Code, w.Body.String())
	}
	if f.count(t, `SELECT count(*) FROM project_memberships WHERE user_id='user-new' AND project_id='FP-A' AND role='viewer'`) != 1 {
		t.Fatal("grant not committed")
	}
	if w := f.requestJSON(pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", "POST", grantBody("user-new", rp("viewer"), nil)); w.Code != 409 {
		t.Fatalf("retry after committed change: %d", w.Code)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_role_changed' AND project_id='FP-A'`); n != 1 {
		t.Fatalf("audits = %d (retry must not re-apply)", n)
	}
	f.api.membersReadHook = nil
	if w := f.request(pm, spm.CsrfToken, "/api/v1/portfolio/projects/FP-A/members", "GET"); w.Code != 200 || !strings.Contains(w.Body.String(), "user-new") {
		t.Fatalf("separate member reload: %d %s", w.Code, w.Body.String())
	}
}
