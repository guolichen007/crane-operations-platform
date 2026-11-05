package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// G2-3: list filters are whitelisted, scoped and share one asOf.
func TestDatabaseG2ListFilters(t *testing.T) {
	f := g1FixtureFor(t)
	g1Exec(t, f.db, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,intake_status,document,evidence_state,protection_basis,next_followup_at)
 SELECT '合成列表'||g,'synthetic list '||g,'synthetic-a','synthetic-a','synthetic-a','11111111-1111-4111-8111-111111111111','DRAFT',
 jsonb_build_object('projectName','合成列表'||g),'INCOMPLETE','LEGACY_CREATED_AT',
 CASE WHEN g%4=0 THEN now()-interval '2 day' WHEN g%4=1 THEN now()+interval '2 day' WHEN g%4=2 THEN now()+interval '9 day' END
 FROM generate_series(1,400) g`)
	g1Create(t, f.admin, "合成列表待分配")
	total := func(c testClient, query string) int {
		t.Helper()
		s, b := c.call("GET", "/api/v1/projects?"+query, nil)
		expect(t, s, 200, b)
		var out struct {
			Total int       `json:"total"`
			AsOf  time.Time `json:"asOf"`
		}
		if err := json.Unmarshal(b, &out); err != nil || out.AsOf.IsZero() {
			t.Fatal("asOf", string(b))
		}
		return out.Total
	}
	over, up, all := total(f.sales, "followup=overdue"), total(f.sales, "followup=upcoming"), total(f.sales, "")
	if over != 100 || up != 100 || all != 400 {
		t.Fatal("windows", over, up, all)
	}
	for _, q := range []string{"owner=synthetic-a", "followup=soon", "assignment=mine", "status=WHATEVER", "stage=L9", "sort=random", "updatedAfter=yesterday"} {
		s, b := f.admin.call("GET", "/api/v1/projects?"+q, nil)
		expect(t, s, 400, b)
		if !strings.Contains(string(b), "INVALID_FILTER") {
			t.Fatal(string(b))
		}
	}
	if n := total(f.admin, "assignment=unassigned"); n != 1 {
		t.Fatal("admin unassigned", n)
	}
	for _, c := range []testClient{f.director, f.manager, f.sales} {
		if n := total(c, "assignment=unassigned"); n != 0 {
			t.Fatal("unassigned leaked", n)
		}
	}
	if n := total(f.other, "q="+url.QueryEscape("合成列表")); n != 0 {
		t.Fatal("cross-owner search", n)
	}
	if n := total(f.sales, "followup=upcoming&sort=followup&pageSize=100"); n != 100 {
		t.Fatal("sorted window", n)
	}
}
