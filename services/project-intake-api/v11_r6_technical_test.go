package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// V1.1 R6 (AC-21, AC-22): technical brief and clarifications. Filing stays
// light; technical data added later never rewrites the filed document or the
// frozen submission; versions, conflicts, rights and history.

func briefOf(t *testing.T, c testClient, id string) TechnicalBrief {
	t.Helper()
	s, b := c.call("GET", "/api/v1/projects/"+id+"/technical-brief", nil)
	expect(t, s, 200, b)
	var out TechnicalBrief
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDatabaseV11TechnicalBrief(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	a1, ma, d1 := f.sales, f.manager, f.director
	c1, mb := login(t, f.server, "synthetic-c"), login(t, f.server, "synthetic-manager-other")
	// AC-21: a filing with only the minimum still works; the brief starts empty.
	s, b := a1.call("POST", "/api/v1/projects", map[string]any{"projectName": "V11技术最小报备", "customer": "V11技术客户", "version": 1})
	minimal := projectFrom(t, s, b)
	if br := briefOf(t, a1, minimal.ID); br.Version != 0 || br.Status != "NOT_STARTED" || !br.Actions["technical.edit"].Allowed {
		t.Fatalf("empty brief %+v", br)
	}
	// A complete filing, submitted: its frozen evidence must never change.
	p := g1Submit(t, a1, g1Create(t, a1, "V11技术需求项目"))
	evidence := func() string {
		var h string
		_ = f.db.QueryRow(ctx, `SELECT (SELECT md5(string_agg(document_hash||revision,',' ORDER BY revision)) FROM project_submissions WHERE project_id=$1)
 ||(SELECT document_hash FROM intake_projects WHERE id=$1)
 ||(SELECT md5(coalesce(string_agg(data::text,',' ORDER BY data::text),'')) FROM planned_equipment WHERE project_id=$1)
 ||(SELECT md5(coalesce(string_agg(category||description,',' ORDER BY category),'')) FROM project_requirements WHERE project_id=$1)`, p.ID).Scan(&h)
		return h
	}
	frozen := evidence()
	in := map[string]any{"version": 0, "status": "COLLECTING", "projectNature": "RETROFIT", "scenario": "炼钢车间铸造起重机改造", "workshop": "炼钢二车间", "productionLine": "2#连铸",
		"siteConstraints": "高温粉尘，检修窗口每月一次", "parameters": []map[string]any{{"key": "定位精度", "value": "±5", "unit": "mm"}, {"key": "节拍", "value": "90", "unit": "s"}},
		"equipment": []map[string]any{{"equipmentType": "铸造起重机", "quantity": 2, "capacityTons": 320, "spanM": 28.5}}, "reason": "技术交流后补录"}
	s, b = a1.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", in)
	expect(t, s, 200, b)
	br := briefOf(t, ma, p.ID)
	if br.Version != 1 || br.Status != "COLLECTING" || len(br.Parameters) != 2 || len(br.Equipment) != 1 || len(br.Revisions) != 1 || br.UpdatedByName != "示例销售 A" {
		t.Fatalf("brief %+v", br)
	}
	// AC-22: filed document, planned equipment, requirements and frozen
	// submissions are unchanged; the filed view still shows the original.
	if evidence() != frozen {
		t.Fatal("technical brief changed filed evidence")
	}
	if eq, _ := json.Marshal(br.Filed["plannedEquipment"]); !strings.Contains(string(eq), `"BRIDGE"`) {
		t.Fatal("filed equipment view", string(eq))
	}
	// Version conflict: a second editor with the old version.
	in["status"] = "CLARIFIED"
	s, b = a1.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", in)
	expect(t, s, 409, b)
	in["version"] = 1
	s, b = a1.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", in)
	expect(t, s, 200, b)
	if n := countRows(t, f.db, `SELECT count(*) FROM project_technical_brief_revisions WHERE project_id='`+p.ID+`'`); n != 2 {
		t.Fatal("revisions", n)
	}
	if _, err := f.db.Exec(ctx, `UPDATE project_technical_brief_revisions SET reason='x'`); !isCode(err, "BB002") {
		t.Fatal("revision mutable", err)
	}
	// Rights: supervisor and director may ask but not edit; other team and
	// other sales cannot even read.
	in["version"] = 2
	for _, c := range []testClient{ma, d1} {
		s, b = c.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", in)
		expect(t, s, 403, b)
	}
	for _, c := range []testClient{c1, mb, f.other} {
		s, b = c.call("GET", "/api/v1/projects/"+p.ID+"/technical-brief", nil)
		expect(t, s, 404, b)
		s, b = c.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions", map[string]any{"question": "越权提问"})
		expect(t, s, 404, b)
	}
	// Invalid input.
	bad := map[string]any{"version": 2, "status": "DONE"}
	s, b = a1.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", bad)
	expect(t, s, 400, b)
	bad = map[string]any{"version": 2, "status": "COLLECTING", "equipment": []map[string]any{{"equipmentType": "x", "quantity": -1}}}
	s, b = a1.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", bad)
	expect(t, s, 400, b)
	s, b = a1.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", map[string]any{"version": 2, "status": "COLLECTING", "role": "baobei_admin"})
	expect(t, s, 400, b)
	// Clarifications: supervisor asks, owner answers, asker closes.
	s, b = ma.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions", map[string]any{"question": "现场是否允许夜间施工？"})
	expect(t, s, 200, b)
	br = briefOf(t, a1, p.ID)
	q := br.Questions[0]
	if q.Status != "OPEN" || q.AskedByName != "示例主管" || q.BriefVersion != 2 || !q.CanAnswer {
		t.Fatalf("question %+v", q)
	}
	s, b = ma.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions/"+q.ID+"/answer", map[string]any{"answer": "代答", "version": q.Version})
	expect(t, s, 403, b)
	s, b = a1.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions/"+q.ID+"/answer", map[string]any{"answer": "允许，22:00 后", "version": q.Version})
	expect(t, s, 200, b)
	s, b = a1.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions/"+q.ID+"/answer", map[string]any{"answer": "重放", "version": q.Version})
	expect(t, s, 409, b)
	q = briefOf(t, ma, p.ID).Questions[0]
	s, b = ma.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions/"+q.ID+"/close", map[string]any{"version": q.Version})
	expect(t, s, 200, b)
	// Reopen by the owner, answer again is possible, close again.
	q = briefOf(t, a1, p.ID).Questions[0]
	if q.Status != "CLOSED" {
		t.Fatal("closed", q.Status)
	}
	s, b = a1.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions/"+q.ID+"/reopen", map[string]any{"version": q.Version})
	expect(t, s, 200, b)
	q = briefOf(t, a1, p.ID).Questions[0]
	if q.Status != "OPEN" || q.Answer == "" {
		t.Fatal("reopen must keep the earlier answer", q.Status, q.Answer)
	}
	s, b = c1.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions/"+q.ID+"/close", map[string]any{"version": q.Version})
	expect(t, s, 404, b)
	s, b = ma.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions/"+q.ID+"/close", map[string]any{"version": q.Version})
	expect(t, s, 200, b)
	if n := countRows(t, f.db, `SELECT count(*) FROM project_technical_question_events WHERE question_id='`+q.ID+`'`); n != 5 {
		t.Fatal("question events", n)
	}
	if _, err := f.db.Exec(ctx, `DELETE FROM project_technical_question_events`); !isCode(err, "BB002") {
		t.Fatal("question events deletable", err)
	}
	// After a transfer the new owner edits; the previous owner can no longer read.
	cur := v11Get(t, ma, p.ID)
	s, b = g1Decision(ma, cur, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
	expect(t, s, 200, b)
	s, b = a1.call("GET", "/api/v1/projects/"+p.ID+"/technical-brief", nil)
	expect(t, s, 404, b)
	in["version"] = 2
	s, b = f.other.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", in)
	expect(t, s, 200, b)
	if evidence() != frozen {
		t.Fatal("evidence changed after technical work")
	}
	// Discarded drafts are read-only.
	s, b = a1.call("POST", "/api/v1/projects/"+minimal.ID+"/discard", map[string]any{"version": minimal.Version, "reason": "测试"})
	expect(t, s, 200, b)
	s, b = a1.call("PUT", "/api/v1/projects/"+minimal.ID+"/technical-brief", map[string]any{"version": 0, "status": "COLLECTING"})
	expect(t, s, 409, b)
}
