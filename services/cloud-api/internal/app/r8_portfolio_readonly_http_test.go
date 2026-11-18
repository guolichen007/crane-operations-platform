package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// R8 read-only negatives for every resource / calendar / schedule / reservation / approve / override
// / cancel route. The portfolio model has two roles (planner, manager; DB CHECK in 00007), so the
// read-only cases are:
//   - a project-only user (ProjectGrant maintainer, no PortfolioGrant): portfolio lists are empty
//     (never another portfolio's rows), every write is 403/404 — nothing is written;
//   - a planner on the manager-only actions (calendar write, approve, override, cancel of an
//     APPROVED item): 403 — nothing is written; the planner still reads everything of its portfolio.
func TestR8PortfolioReadOnlyNegativesHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	portfolioSecurityData(t, f)
	outsider, so := f.login(t, "maintainer") // ProjectGrant P001 maintainer, no portfolio membership
	planner, sp := f.login(t, "a")           // portfolio_planner on PS-A
	manager, sm := f.login(t, "pm")          // portfolio_manager on PS-A
	snapshot := func() string {
		var s string
		if err := f.db.QueryRow(`SELECT concat_ws(',', (SELECT count(*) FROM resources), (SELECT count(*) FROM resource_calendars), (SELECT count(*) FROM resource_calendar_rules),
		  (SELECT count(*) FROM schedule_items), (SELECT string_agg(id||':'||status||':'||version, ';' ORDER BY id) FROM schedule_items), (SELECT count(*) FROM resource_reservations),
		  (SELECT string_agg(id||':'||status, ';' ORDER BY id) FROM resource_reservations), (SELECT count(*) FROM schedule_dependencies), (SELECT count(*) FROM schedule_baselines),
		  (SELECT string_agg(id::text||':'||status, ';' ORDER BY id) FROM schedule_conflicts), (SELECT count(*) FROM schedule_conflict_overrides))`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// The manager prepares one PENDING reservation on SI-A (to have something to cancel).
	var res struct{ Id string }
	w := f.doBody(manager, sm.CsrfToken, "POST", "/api/v1/portfolio/schedule/items/SI-A/reservations", nil, map[string]any{"resourceId": "R-A", "startAt": nil, "endAt": nil, "quantity": 1})
	if w.Code != 422 { // nil range is invalid input, proving validation runs for an authorized writer
		t.Fatalf("invalid reservation = %d %s", w.Code, w.Body.String())
	}
	var item struct {
		StartAt, EndAt string
	}
	_ = json.Unmarshal(f.request(manager, sm.CsrfToken, "/api/v1/portfolio/schedule/items/SI-A", "GET").Body.Bytes(), &item)
	w = f.doBody(manager, sm.CsrfToken, "POST", "/api/v1/portfolio/schedule/items/SI-A/reservations", nil, map[string]any{"resourceId": "R-A", "startAt": item.StartAt, "endAt": item.EndAt, "quantity": 1})
	if w.Code != 201 {
		t.Fatalf("manager reservation = %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	var conflictID int
	if err := f.db.QueryRow(`SELECT id FROM schedule_conflicts WHERE left_item_id='SI-A'`).Scan(&conflictID); err != nil {
		t.Fatal(err)
	}
	before := snapshot()

	cal := map[string]any{"timezone": "Asia/Shanghai", "version": 0, "rules": []map[string]any{{"weekday": 1, "startLocal": "08:00", "endLocal": "18:00", "availability": "AVAILABLE"}}}
	writes := []struct {
		method, path string
		body         any
		ifMatch      string
	}{
		{"POST", "/api/v1/portfolio/resources", map[string]any{"portfolioScopeId": "PS-A", "resourceType": "TEAM", "name": "x", "capacity": 1}, ""},
		{"PUT", "/api/v1/portfolio/resources/R-A/calendar", cal, ""},
		{"POST", "/api/v1/portfolio/schedule/items", map[string]any{"portfolioScopeId": "PS-A", "sourceType": "COMMISSIONING", "name": "x", "startAt": item.StartAt, "endAt": item.EndAt}, ""},
		{"POST", "/api/v1/portfolio/schedule/items/SI-A/reservations", map[string]any{"resourceId": "R-A", "startAt": item.StartAt, "endAt": item.EndAt, "quantity": 1}, ""},
		{"POST", "/api/v1/portfolio/reservations/" + res.Id + "/cancel", map[string]any{}, "1"},
		{"POST", "/api/v1/portfolio/schedule/items/SI-A/cancel", map[string]any{"reason": "只读用户尝试取消"}, "1"},
		{"POST", "/api/v1/portfolio/schedule/dependencies", map[string]any{"predecessorItemId": "SI-A", "successorItemId": "SI-A", "dependencyType": "FS", "lagMinutes": 0}, ""},
		{"POST", "/api/v1/portfolio/schedule/approve", map[string]any{"itemIds": []string{"SI-A"}}, ""},
		{"POST", fmt.Sprintf("/api/v1/portfolio/conflicts/%d/override", conflictID), map[string]any{"reason": "只读用户尝试覆盖冲突"}, ""},
	}
	do := func(c *http.Cookie, csrf string, i int) int {
		h := map[string]string{}
		if writes[i].ifMatch != "" {
			h["If-Match"] = writes[i].ifMatch
		}
		return f.doBody(c, csrf, writes[i].method, writes[i].path, h, writes[i].body).Code
	}
	// 1. Project-only user: every write refused (403, or 404 for a child object it cannot see).
	for i := range writes {
		if code := do(outsider, so.CsrfToken, i); code != 403 && code != 404 {
			t.Errorf("outsider %s %s = %d, want 403/404", writes[i].method, writes[i].path, code)
		}
	}
	// …and every read is empty / not found (no other portfolio's data).
	for _, path := range []string{"/api/v1/portfolio/resources", "/api/v1/portfolio/schedule/items", "/api/v1/portfolio/conflicts", "/api/v1/portfolio/baselines", "/api/v1/portfolio/schedule/dependencies"} {
		var out []json.RawMessage
		w := f.request(outsider, so.CsrfToken, path, "GET")
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code == 200 && len(out) != 0 || w.Code != 200 && w.Code != 403 {
			t.Errorf("outsider GET %s = %d %d items", path, w.Code, len(out))
		}
	}
	for _, path := range []string{"/api/v1/portfolio/resources/R-A", "/api/v1/portfolio/resources/R-A/calendar", "/api/v1/portfolio/schedule/items/SI-A", "/api/v1/portfolio/schedule/items/SI-A/reservations"} {
		if code := f.request(outsider, so.CsrfToken, path, "GET").Code; code != 404 && code != 403 {
			t.Errorf("outsider GET %s = %d, want 404/403", path, code)
		}
	}
	// 2. Planner: reads everything of PS-A, but manager-only actions are 403.
	for _, path := range []string{"/api/v1/portfolio/resources/R-A", "/api/v1/portfolio/resources/R-A/calendar", "/api/v1/portfolio/schedule/items/SI-A", "/api/v1/portfolio/schedule/items/SI-A/reservations", fmt.Sprintf("/api/v1/portfolio/conflicts/%d", conflictID)} {
		if code := f.request(planner, sp.CsrfToken, path, "GET").Code; code != 200 {
			t.Errorf("planner GET %s = %d, want 200", path, code)
		}
	}
	for _, i := range []int{1, 7, 8} { // calendar write, approve, override
		if code := do(planner, sp.CsrfToken, i); code != 403 {
			t.Errorf("planner %s %s = %d, want 403", writes[i].method, writes[i].path, code)
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("refused writes changed data:\n before %s\n after  %s", before, after)
	}
	// Cancel of an APPROVED item is manager-only (approve as manager first, then the planner tries).
	if _, err := f.db.Exec(`DELETE FROM resource_reservations; DELETE FROM schedule_conflicts WHERE left_item_id='SI-A'`); err != nil {
		t.Fatal(err)
	}
	if w := f.doBody(manager, sm.CsrfToken, "POST", "/api/v1/portfolio/schedule/approve", nil, map[string]any{"itemIds": []string{"SI-A"}}); w.Code != 200 {
		t.Fatalf("manager approve = %d %s", w.Code, w.Body.String())
	}
	var version int
	_ = f.db.QueryRow(`SELECT version FROM schedule_items WHERE id='SI-A'`).Scan(&version)
	approvedBefore := snapshot()
	if w := f.doBody(planner, sp.CsrfToken, "POST", "/api/v1/portfolio/schedule/items/SI-A/cancel", map[string]string{"If-Match": fmt.Sprint(version)}, map[string]any{"reason": "规划员尝试取消已批准排期"}); w.Code != 403 {
		t.Fatalf("planner cancel approved = %d %s", w.Code, w.Body.String())
	}
	if w := f.doBody(outsider, so.CsrfToken, "POST", "/api/v1/portfolio/schedule/items/SI-A/cancel", map[string]string{"If-Match": fmt.Sprint(version)}, map[string]any{"reason": "项目用户尝试取消已批准排期"}); w.Code != 403 && w.Code != 404 {
		t.Fatalf("outsider cancel approved = %d", w.Code)
	}
	if after := snapshot(); after != approvedBefore {
		t.Fatalf("refused cancel changed data:\n before %s\n after  %s", approvedBefore, after)
	}
}
