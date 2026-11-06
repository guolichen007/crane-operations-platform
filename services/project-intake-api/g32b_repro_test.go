package main

import (
	"encoding/json"
	"testing"
)

// G3-2B reproduction using only the pre-G3-2B HTTP surface: the home tile
// "你可独立审核" and the review list it links to must describe the same set.
// On 07215c9 the list ignores eligibility and returns every pending item.
func TestDatabaseG32BHomeListMismatch(t *testing.T) {
	f := g1FixtureFor(t)
	for _, n := range []string{"一", "二", "三"} {
		g1Submit(t, f.sales, g1Create(t, f.sales, "合成复现可审"+n))
	}
	// Seven the manager cannot decide: four the manager handed off before submission,
	// three whose current evidence is incomplete.
	for _, n := range []string{"一", "二", "三", "四"} {
		p := g1Create(t, f.sales, "合成复现参与"+n)
		s, b := g1Decision(f.manager, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
		g1Submit(t, f.other, projectFrom(t, s, b))
	}
	for _, n := range []string{"一", "二", "三"} {
		p := g1Submit(t, f.sales, g1Create(t, f.sales, "合成复现证据"+n))
		g1Exec(t, f.db, `UPDATE intake_projects SET evidence_state='INCOMPLETE' WHERE id=$1`, p.ID)
	}
	s, b := f.manager.call("GET", "/api/v1/workspace-summary", nil)
	expect(t, s, 200, b)
	var home WorkspaceSummary
	_ = json.Unmarshal(b, &home)
	s, b = f.manager.call("GET", "/api/v1/reviews?kind=pending&eligibility=reviewable", nil)
	expect(t, s, 200, b)
	var list struct {
		Items []Project
		Total int
	}
	_ = json.Unmarshal(b, &list)
	t.Logf("REPRO home.reviewable=%d home.reviewQueue=%d list(eligibility=reviewable).total=%d", home.Counts["reviewable"], home.Counts["reviewQueue"], list.Total)
	if home.Counts["reviewable"] != 3 || home.Counts["reviewQueue"] != 10 {
		t.Fatal("fixture", home.Counts)
	}
	if list.Total != home.Counts["reviewable"] {
		t.Fatalf("首页“你可独立审核”=%d，跳转后的审核列表 total=%d", home.Counts["reviewable"], list.Total)
	}
	for _, it := range list.Items {
		if !it.Actions["approve"].Allowed {
			t.Fatalf("列表中出现本人无法审核的项目 %s（%v）", it.RegistrationNo, code(it.Actions["approve"]))
		}
	}
}
