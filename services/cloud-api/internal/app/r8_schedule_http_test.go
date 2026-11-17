package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

// R8 resource & schedule workflow through the real BFF: planner (a, PS-A) creates resources,
// calendar, schedule items and reservations; a blocking conflict is reported with details; the
// manager (pm) overrides an overridable conflict, a hard one cannot be; approval writes ACTIVE
// reservations and an immutable baseline; an approved item can only be cancelled by the manager
// with If-Match and a reason, keeping its history; other portfolios (b, PS-B) see and touch nothing.
func TestR8ScheduleWorkflowHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	portfolioSecurityData(t, f)
	planner, ps := f.login(t, "a")
	manager, ms := f.login(t, "pm")
	other, os := f.login(t, "b")
	post := func(c *http.Cookie, csrf, path string, body any, headers map[string]string) (int, string) {
		w := f.doBody(c, csrf, "POST", path, headers, body)
		return w.Code, w.Body.String()
	}
	get := func(c *http.Cookie, csrf, path string, out any) int {
		w := f.request(c, csrf, path, "GET")
		if out != nil {
			_ = json.Unmarshal(w.Body.Bytes(), out)
		}
		return w.Code
	}

	// Resources: invalid input 422, cross-portfolio 403, valid 201.
	for name, bad := range map[string]map[string]any{
		"blank name":         {"portfolioScopeId": "PS-A", "resourceType": "RUNWAY", "name": " ", "capacity": 1},
		"unknown type":       {"portfolioScopeId": "PS-A", "resourceType": "ROCKET", "name": "x", "capacity": 1},
		"exclusive capacity": {"portfolioScopeId": "PS-A", "resourceType": "RUNWAY", "name": "x", "capacity": 2, "exclusive": true},
		"zero capacity":      {"portfolioScopeId": "PS-A", "resourceType": "TEAM", "name": "x", "capacity": 0},
		"bad timezone":       {"portfolioScopeId": "PS-A", "resourceType": "TEAM", "name": "x", "capacity": 1, "homeTimezone": "Mars/Base"},
	} {
		if c, b := post(planner, ps.CsrfToken, "/api/v1/portfolio/resources", bad, nil); c != 422 {
			t.Errorf("resource %s = %d %s", name, c, b)
		}
	}
	if c, _ := post(other, os.CsrfToken, "/api/v1/portfolio/resources", map[string]any{"portfolioScopeId": "PS-A", "resourceType": "TEAM", "name": "x", "capacity": 1}, nil); c != 403 {
		t.Fatalf("PS-B planner created a PS-A resource: %d", c)
	}
	mk := func(name string, exclusive, override bool, capacity int) contract.Resource {
		c, b := post(planner, ps.CsrfToken, "/api/v1/portfolio/resources", map[string]any{"portfolioScopeId": "PS-A", "resourceType": "TEAM", "name": name, "capacity": capacity, "exclusive": exclusive, "overrideAllowed": override}, nil)
		if c != 201 {
			t.Fatalf("create resource %s = %d %s", name, c, b)
		}
		var r contract.Resource
		_ = json.Unmarshal([]byte(b), &r)
		if r.Status != "ACTIVE" || r.Version != 1 || r.HomeTimezone != "Asia/Shanghai" {
			t.Fatalf("resource defaults = %+v", r)
		}
		return r
	}
	crew := mk("调试班组", true, true, 1)
	runway := mk("R8 轨道窗口", true, false, 1)
	var resources []contract.Resource
	if get(other, os.CsrfToken, "/api/v1/portfolio/resources", &resources); strings.Contains(fmt.Sprint(resources), crew.Id) {
		t.Fatal("PS-B sees PS-A resource")
	}

	// Calendar: planner cannot write (resource_calendar_write is manager-only); manager writes with
	// version; a stale version is 409; rules read back.
	cal := map[string]any{"timezone": "Asia/Shanghai", "version": 0, "rules": []map[string]any{{"weekday": 1, "startLocal": "08:00", "endLocal": "18:00", "availability": "AVAILABLE"}}}
	if c, _ := f.doBody(planner, ps.CsrfToken, "PUT", "/api/v1/portfolio/resources/"+crew.Id+"/calendar", nil, cal).Code, 0; c != 403 {
		t.Fatalf("planner wrote a calendar: %d", c)
	}
	w := f.doBody(manager, ms.CsrfToken, "PUT", "/api/v1/portfolio/resources/"+crew.Id+"/calendar", nil, cal)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"startLocal":"08:00"`) {
		t.Fatalf("calendar write = %d %s", w.Code, w.Body.String())
	}
	if c := f.doBody(manager, ms.CsrfToken, "PUT", "/api/v1/portfolio/resources/"+crew.Id+"/calendar", nil, cal).Code; c != 409 {
		t.Fatalf("stale calendar version = %d, want 409", c)
	}
	badRule := map[string]any{"timezone": "Asia/Shanghai", "version": 1, "rules": []map[string]any{{"weekday": 9, "startLocal": "08:00", "endLocal": "07:00", "availability": "AVAILABLE"}}}
	if c := f.doBody(manager, ms.CsrfToken, "PUT", "/api/v1/portfolio/resources/"+crew.Id+"/calendar", nil, badRule).Code; c != 422 {
		t.Fatalf("invalid calendar rule = %d, want 422", c)
	}

	// Two overlapping items on the same exclusive resources (a Monday in the calendar window).
	loc, _ := time.LoadLocation("Asia/Shanghai")
	monday := time.Date(2027, 3, 1, 9, 0, 0, 0, loc) // 2027-03-01 is a Monday
	item := func(name string, start time.Time, hours int) contract.ScheduleItem {
		c, b := post(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/items", map[string]any{"portfolioScopeId": "PS-A", "sourceType": "COMMISSIONING", "name": name, "startAt": start, "endAt": start.Add(time.Duration(hours) * time.Hour)}, nil)
		if c != 201 {
			t.Fatalf("create item %s = %d %s", name, c, b)
		}
		var it contract.ScheduleItem
		_ = json.Unmarshal([]byte(b), &it)
		return it
	}
	if c, b := post(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/items", map[string]any{"portfolioScopeId": "PS-A", "sourceType": "COMMISSIONING", "name": "反", "startAt": monday, "endAt": monday}, nil); c != 422 {
		t.Fatalf("zero-length item = %d %s", c, b)
	}
	first := item("一号行车调试", monday, 4)
	second := item("二号行车调试", monday.Add(2*time.Hour), 4)
	reserve := func(it contract.ScheduleItem, r contract.Resource, start time.Time, hours int) (int, string) {
		return post(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/items/"+it.Id+"/reservations", map[string]any{"resourceId": r.Id, "startAt": start, "endAt": start.Add(time.Duration(hours) * time.Hour), "quantity": 1}, nil)
	}
	if c, b := reserve(first, crew, monday.Add(-time.Hour), 2); c != 422 {
		t.Fatalf("reservation outside the item range = %d %s", c, b)
	}
	if c, _ := post(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/items/"+first.Id+"/reservations", map[string]any{"resourceId": "R-B", "startAt": monday, "endAt": monday.Add(time.Hour), "quantity": 1}, nil); c != 409 {
		t.Fatalf("cross-portfolio reservation = %d, want 409", c)
	}
	for _, it := range []contract.ScheduleItem{first, second} {
		if c, b := reserve(it, crew, it.StartAt, 4); c != 201 {
			t.Fatalf("reserve crew = %d %s", c, b)
		}
	}
	if c, b := reserve(second, runway, second.StartAt, 4); c != 201 {
		t.Fatalf("reserve runway = %d %s", c, b)
	}
	if c, b := reserve(first, runway, first.StartAt, 4); c != 201 {
		t.Fatalf("reserve runway first = %d %s", c, b)
	}

	approve := func(c *http.Cookie, csrf string, ids ...string) (int, string) {
		return post(c, csrf, "/api/v1/portfolio/schedule/approve", map[string]any{"itemIds": ids}, nil)
	}
	if c, b := approve(manager, ms.CsrfToken, first.Id); c != 200 {
		t.Fatalf("approve first = %d %s", c, b)
	}
	// The second overlaps on both exclusive resources: 409 with conflict details, nothing approved.
	c, b := approve(manager, ms.CsrfToken, second.Id)
	if c != 409 || !strings.Contains(b, `"code":"schedule_conflict"`) || !strings.Contains(b, "RESOURCE_OVERLAP") {
		t.Fatalf("conflicting approval = %d %s", c, b)
	}
	var conflicts []contract.ScheduleConflict
	get(planner, ps.CsrfToken, "/api/v1/portfolio/conflicts", &conflicts)
	var crewConflict, runwayConflict int
	for _, cf := range conflicts {
		if cf.LeftItemId == second.Id && cf.ResourceId != nil && *cf.ResourceId == crew.Id {
			crewConflict = cf.Id
		}
		if cf.LeftItemId == second.Id && cf.ResourceId != nil && *cf.ResourceId == runway.Id {
			runwayConflict = cf.Id
		}
	}
	if crewConflict == 0 || runwayConflict == 0 {
		t.Fatalf("open conflicts not listed: %+v", conflicts)
	}
	var otherConflicts []contract.ScheduleConflict
	get(other, os.CsrfToken, "/api/v1/portfolio/conflicts", &otherConflicts)
	for _, cf := range otherConflicts {
		if cf.LeftItemId == second.Id {
			t.Fatal("PS-B sees PS-A conflict")
		}
	}
	// Planner cannot override; manager: short reason 422, hard (runway) 409, crew overridable 200.
	if c, _ := post(planner, ps.CsrfToken, fmt.Sprintf("/api/v1/portfolio/conflicts/%d/override", crewConflict), map[string]any{"reason": "现场确认可以共用班组"}, nil); c != 403 {
		t.Fatalf("planner override = %d", c)
	}
	if c, _ := post(manager, ms.CsrfToken, fmt.Sprintf("/api/v1/portfolio/conflicts/%d/override", crewConflict), map[string]any{"reason": "短"}, nil); c != 422 {
		t.Fatalf("short reason = %d", c)
	}
	if c, _ := post(manager, ms.CsrfToken, fmt.Sprintf("/api/v1/portfolio/conflicts/%d/override", runwayConflict), map[string]any{"reason": "试图覆盖硬冲突的理由"}, nil); c != 409 {
		t.Fatalf("hard override = %d, want 409", c)
	}
	if c, b := post(manager, ms.CsrfToken, fmt.Sprintf("/api/v1/portfolio/conflicts/%d/override", crewConflict), map[string]any{"reason": "现场确认两台调试可以共用班组"}, nil); c != 200 {
		t.Fatalf("override = %d %s", c, b)
	}
	// Still blocked by the hard runway overlap; withdraw the runway reservation (planner), then approve.
	if c, _ := approve(manager, ms.CsrfToken, second.Id); c != 409 {
		t.Fatalf("approval with remaining hard conflict = %d", c)
	}
	var res []contract.ResourceReservation
	get(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/items/"+second.Id+"/reservations", &res)
	var runwayRes contract.ResourceReservation
	for _, r := range res {
		if r.ResourceId == runway.Id {
			runwayRes = r
		}
	}
	if c, _ := post(planner, ps.CsrfToken, "/api/v1/portfolio/reservations/"+runwayRes.Id+"/cancel", map[string]any{}, nil); c != 428 {
		t.Fatalf("cancel without If-Match = %d", c)
	}
	if c, b := post(planner, ps.CsrfToken, "/api/v1/portfolio/reservations/"+runwayRes.Id+"/cancel", map[string]any{}, map[string]string{"If-Match": strconv.FormatInt(runwayRes.Version, 10)}); c != 200 {
		t.Fatalf("cancel reservation = %d %s", c, b)
	}
	if c, b := approve(manager, ms.CsrfToken, second.Id); c != 200 {
		t.Fatalf("approve after override + withdraw = %d %s", c, b)
	}
	// Double approval (repeat submit) is a 409 state conflict, never a second approval.
	if c, _ := approve(manager, ms.CsrfToken, second.Id); c != 409 {
		t.Fatalf("repeat approval = %d, want 409", c)
	}
	var baselines []contract.ScheduleBaseline
	get(planner, ps.CsrfToken, "/api/v1/portfolio/baselines", &baselines)
	if len(baselines) != 2 || baselines[0].BaselineVersion != 2 || baselines[0].ApprovedBy != "user-pm" {
		t.Fatalf("baselines = %+v", baselines)
	}
	var snapshot string
	_ = f.db.QueryRow(`SELECT snapshot_json::text FROM schedule_baselines WHERE baseline_version=2 AND portfolio_scope_id='PS-A'`).Scan(&snapshot)
	if !strings.Contains(snapshot, second.Id) || !strings.Contains(snapshot, crew.Id) || strings.Contains(snapshot, runwayRes.Id) {
		t.Fatalf("baseline snapshot = %s", snapshot)
	}
	// Approved item: planner cannot cancel; manager without If-Match 428; with reason + version 200.
	var approved contract.ScheduleItem
	get(manager, ms.CsrfToken, "/api/v1/portfolio/schedule/items/"+second.Id, &approved)
	if approved.Status != "APPROVED" {
		t.Fatalf("item = %+v", approved)
	}
	if c, _ := post(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/items/"+second.Id+"/cancel", map[string]any{"reason": "客户延期"}, map[string]string{"If-Match": strconv.FormatInt(approved.Version, 10)}); c != 403 {
		t.Fatalf("planner cancelled an approved item: %d", c)
	}
	if c, _ := post(manager, ms.CsrfToken, "/api/v1/portfolio/schedule/items/"+second.Id+"/cancel", map[string]any{"reason": "客户延期"}, map[string]string{"If-Match": "1"}); c != 409 {
		t.Fatalf("stale cancel = %d", c)
	}
	if c, b := post(manager, ms.CsrfToken, "/api/v1/portfolio/schedule/items/"+second.Id+"/cancel", map[string]any{"reason": "客户延期至四月"}, map[string]string{"If-Match": strconv.FormatInt(approved.Version, 10)}); c != 200 {
		t.Fatalf("cancel approved = %d %s", c, b)
	}
	var after int
	_ = f.db.QueryRow(`SELECT count(*) FROM schedule_baselines WHERE portfolio_scope_id='PS-A'`).Scan(&after)
	var active int
	_ = f.db.QueryRow(`SELECT count(*) FROM resource_reservations WHERE schedule_item_id=$1 AND status='ACTIVE'`, second.Id).Scan(&active)
	if after != 2 || active != 0 {
		t.Fatalf("cancel must keep baselines (%d) and release reservations (%d active)", after, active)
	}
	// PS-B cannot read PS-A objects directly.
	if c := get(other, os.CsrfToken, "/api/v1/portfolio/schedule/items/"+first.Id+"/reservations", nil); c != 404 {
		t.Fatalf("cross-portfolio reservations read = %d", c)
	}
	// Dependencies: cross-portfolio refused, own scope cycle 409.
	if c, _ := post(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/dependencies", map[string]any{"predecessorItemId": first.Id, "successorItemId": "SI-B", "dependencyType": "FS", "lagMinutes": 0}, nil); c != 409 {
		t.Fatalf("cross-portfolio dependency = %d", c)
	}
	third := item("三号", monday.Add(24*time.Hour), 2)
	if c, b := post(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/dependencies", map[string]any{"predecessorItemId": first.Id, "successorItemId": third.Id, "dependencyType": "FS", "lagMinutes": 0}, nil); c != 201 {
		t.Fatalf("dependency = %d %s", c, b)
	}
	if c, _ := post(planner, ps.CsrfToken, "/api/v1/portfolio/schedule/dependencies", map[string]any{"predecessorItemId": third.Id, "successorItemId": first.Id, "dependencyType": "FS", "lagMinutes": 0}, nil); c != 409 {
		t.Fatalf("dependency cycle = %d", c)
	}
}
