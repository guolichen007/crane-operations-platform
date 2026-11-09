package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// G3-3A: real stage pipeline. Every count comes from the same projectScope and
// filters as GET /projects; a stage column's total equals the summary count in
// the same data state; paging is stable by updated_at then id.

func stageSummaryOf(t *testing.T, c testClient, query string) StageSummary {
	t.Helper()
	s, b := c.call("GET", "/api/v1/stage-summary?"+query, nil)
	expect(t, s, 200, b)
	var out StageSummary
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Stages) != 12 || out.AsOf.IsZero() || out.Scope == "" {
		t.Fatalf("shape: %s", b)
	}
	for i, code := range stages {
		if out.Stages[i].Code != code {
			t.Fatalf("stage order %d %s", i, out.Stages[i].Code)
		}
	}
	return out
}

type listPage struct {
	Items []Project `json:"items"`
	Total int       `json:"total"`
}

func listOf(t *testing.T, c testClient, query string) listPage {
	t.Helper()
	s, b := c.call("GET", "/api/v1/projects?"+query, nil)
	expect(t, s, 200, b)
	var out listPage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// createStaged creates a draft through the API with a given stage, province
// and unique customer (no duplicate detection between fixtures).
func createStaged(t *testing.T, c testClient, name, stage, province string) Project {
	t.Helper()
	in := sample(name)
	in.Customer = name + "客户"
	in.SalesStage = stage
	in.Location.Province = province
	in.Location.FactoryName = name
	s, b := c.call("POST", "/api/v1/projects", in)
	return projectFrom(t, s, b)
}

// assertStageEquivalence: for every stage, summary count == /projects?stage
// total under the same base filters, and sum == unfiltered-by-stage total.
func assertStageEquivalence(t *testing.T, label string, c testClient, base string) StageSummary {
	t.Helper()
	sum := stageSummaryOf(t, c, base)
	join := func(extra string) string {
		if base == "" {
			return extra
		}
		return base + "&" + extra
	}
	all := listOf(t, c, join("pageSize=1"))
	acc := 0
	for _, sc := range sum.Stages {
		got := listOf(t, c, join("stage="+sc.Code+"&pageSize=1")).Total
		if got != sc.Count {
			t.Fatalf("%s %s: summary %d list %d", label, sc.Code, sc.Count, got)
		}
		acc += sc.Count
	}
	if acc+sum.Unclassified != sum.Total || sum.Total != all.Total {
		t.Fatalf("%s: stages %d + unclassified %d, total %d, list total %d", label, acc, sum.Unclassified, sum.Total, all.Total)
	}
	return sum
}

func TestDatabaseG33AStageSummary(t *testing.T) {
	ctx := context.Background()

	t.Run("four-roles-all-stages-and-intake-states", func(t *testing.T) {
		f := g1FixtureFor(t)
		c := login(t, f.server, "synthetic-c")
		otherManager := login(t, f.server, "synthetic-manager-other")
		// Every stage for sales A (team one), alternating provinces.
		for i, code := range stages {
			createStaged(t, f.sales, fmt.Sprintf("合成管道A%02d", i), code, []string{"江苏省", "浙江省"}[i%2])
		}
		// The same stage (L3) in four intake states.
		l3 := map[string]Project{}
		for _, st := range []string{"DRAFT", "SUBMITTED", "RETURNED", "APPROVED"} {
			p := createStaged(t, f.sales, "合成管道状态"+st, "L3_SITE_SURVEY", "江苏省")
			if st != "DRAFT" {
				p = g1Submit(t, f.sales, p)
			}
			if st == "RETURNED" || st == "APPROVED" {
				s, b := g1Decision(f.manager, p, map[string]string{"RETURNED": "return", "APPROVED": "approve"}[st], nil)
				p = projectFrom(t, s, b)
			}
			if p.IntakeStatus != st {
				t.Fatal("fixture status", st, p.IntakeStatus)
			}
			l3[st] = p
		}
		// Sales B (team one), sales C (team two), an admin unassigned draft.
		createStaged(t, f.other, "合成管道B", "L5_QUOTATION", "江苏省")
		createStaged(t, c, "合成管道C1", "L5_QUOTATION", "江苏省")
		createStaged(t, c, "合成管道C2", "WON", "浙江省")
		g1Create(t, f.admin, "合成管道待分配")
		// L4 left empty for sales B and team two.

		type exp struct {
			total int
			stage map[string]int
		}
		want := map[string]exp{
			"sales-a":       {16, map[string]int{"L3_SITE_SURVEY": 5, "L5_QUOTATION": 1, "WON": 1, "LOST": 1, "HOLD": 1}},
			"sales-b":       {1, map[string]int{"L5_QUOTATION": 1, "L4_SOLUTION": 0}},
			"manager":       {17, map[string]int{"L5_QUOTATION": 2, "L3_SITE_SURVEY": 5}},
			"other-manager": {2, map[string]int{"L5_QUOTATION": 1, "WON": 1, "L4_SOLUTION": 0}},
			"director":      {19, map[string]int{"L5_QUOTATION": 3, "WON": 2}},
			"admin":         {20, map[string]int{"L1_REQUIREMENT": 2}}, // unassigned draft defaults to L1 (sample)
		}
		clients := map[string]testClient{"sales-a": f.sales, "sales-b": f.other, "manager": f.manager, "other-manager": otherManager, "director": f.director, "admin": f.admin}
		scopes := map[string]string{"sales-a": "OWN", "sales-b": "OWN", "manager": "TEAM", "other-manager": "TEAM", "director": "ALL_ASSIGNED", "admin": "ALL"}
		for who, c := range clients {
			sum := assertStageEquivalence(t, who, c, "")
			if sum.Total != want[who].total || sum.Scope != scopes[who] || sum.Unclassified != 0 {
				t.Fatalf("%s total %d scope %s want %d %s", who, sum.Total, sum.Scope, want[who].total, scopes[who])
			}
			for code, n := range want[who].stage {
				for _, sc := range sum.Stages {
					if sc.Code == code && sc.Count != n {
						t.Fatalf("%s %s=%d want %d", who, code, sc.Count, n)
					}
				}
			}
		}
		// Sales never sees another owner's project through a stage column.
		for _, p := range listOf(t, f.other, "stage=L5_QUOTATION&pageSize=100").Items {
			if p.OwnerSubject != "synthetic-b" {
				t.Fatal("sales saw", p.OwnerSubject)
			}
		}
		// Stage and intake status are independent: L3 holds all four intake states.
		seen := map[string]bool{}
		for _, p := range listOf(t, f.manager, "stage=L3_SITE_SURVEY&pageSize=100").Items {
			seen[p.IntakeStatus] = true
			if p.SalesStage != "L3_SITE_SURVEY" {
				t.Fatal("wrong column", p.SalesStage)
			}
		}
		for st := range l3 {
			if !seen[st] {
				t.Fatal("missing intake state in stage column", st)
			}
		}
		// APPROVED is an intake state, never a stage; L8 is not "approved".
		for _, sc := range stageSummaryOf(t, f.director, "").Stages {
			if sc.Code == "APPROVED" {
				t.Fatal("intake status leaked into stages")
			}
		}
	})

	t.Run("base-filters-apply-equally", func(t *testing.T) {
		f := g1FixtureFor(t)
		for i, code := range stages {
			createStaged(t, f.sales, fmt.Sprintf("合成筛选甲%02d", i), code, "江苏省")
			createStaged(t, f.sales, fmt.Sprintf("合成筛选乙%02d", i), code, "浙江省")
		}
		p := g1Submit(t, f.sales, createStaged(t, f.sales, "合成筛选已提交", "L2_TECHNICAL", "江苏省"))
		later := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
		time.Sleep(1100 * time.Millisecond)
		createStaged(t, f.sales, "合成筛选新", "L6_TENDER", "浙江省")
		for _, base := range []string{
			"",
			"q=" + url.QueryEscape("合成筛选甲"),
			"province=" + url.QueryEscape("浙江省"),
			"status=SUBMITTED",
			"status=DRAFT&province=" + url.QueryEscape("江苏省"),
			"updatedAfter=" + url.QueryEscape(later),
			"q=" + url.QueryEscape(p.RegistrationNo),
		} {
			for _, c := range []testClient{f.sales, f.manager, f.director, f.admin} {
				assertStageEquivalence(t, base, c, base)
			}
		}
		if n := stageSummaryOf(t, f.manager, "province="+url.QueryEscape("浙江省")).Total; n != 13 {
			t.Fatal("province filter", n)
		}
		if n := stageSummaryOf(t, f.manager, "status=SUBMITTED").Total; n != 1 {
			t.Fatal("status filter", n)
		}
	})

	t.Run("invalid-parameters-are-rejected", func(t *testing.T) {
		f := g1FixtureFor(t)
		for _, q := range []string{"stage=L3_SITE_SURVEY", "page=1", "pageSize=20", "sort=name", "followup=overdue", "owner=x", "status=WHATEVER", "status=WON", "updatedAfter=yesterday", "q=a&q=b"} {
			s, b := f.manager.call("GET", "/api/v1/stage-summary?"+q, nil)
			expect(t, s, 400, b)
			if !strings.Contains(string(b), "INVALID_FILTER") {
				t.Fatal(q, string(b))
			}
		}
		for _, q := range []string{"stage=L9", "stage=APPROVED", "page=0", "page=-1", "page=x", "pageSize=0", "pageSize=101", "pageSize=x"} {
			s, b := f.manager.call("GET", "/api/v1/projects?"+q, nil)
			expect(t, s, 400, b)
		}
		s, b := f.manager.call("GET", "/api/v1/projects?stage=WON&page=3&pageSize=100", nil)
		expect(t, s, 200, b)
		s, b = login(t, f.server, "synthetic-a").call("GET", "/api/v1/stage-summary", nil)
		expect(t, s, 200, b)
	})

	t.Run("pagination-over-1001-stable-ties", func(t *testing.T) {
		f := g1FixtureFor(t)
		// 1 API project + 1105 synthetic rows in L4, all with the same
		// updated_at so only the id orders them (synthetic, SQL).
		seed := createStaged(t, f.sales, "合成分页种子", "L4_SOLUTION", "江苏省")
		g1Exec(t, f.db, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,intake_status,sales_stage,document,evidence_state,protection_basis,updated_at)
 SELECT '合成分页'||g,'synthetic paging '||g,'synthetic-a','synthetic-a','synthetic-a','11111111-1111-4111-8111-111111111111','DRAFT','L4_SOLUTION',
 jsonb_build_object('projectName','合成分页'||g),'INCOMPLETE','LEGACY_CREATED_AT','2026-01-01T00:00:00Z' FROM generate_series(1,1105) g`)
		g1Exec(t, f.db, `UPDATE intake_projects SET updated_at='2026-01-01T00:00:00Z' WHERE id=$1`, seed.ID)
		sum := assertStageEquivalence(t, "1106", f.manager, "")
		var l4 int
		for _, sc := range sum.Stages {
			if sc.Code == "L4_SOLUTION" {
				l4 = sc.Count
			}
		}
		if l4 != 1106 {
			t.Fatal("L4 count", l4)
		}
		for _, size := range []int{20, 100, 7} {
			seen := map[string]bool{}
			last := ""
			pages := (l4 + size - 1) / size
			for pg := 1; pg <= pages+1; pg++ {
				out := listOf(t, f.manager, fmt.Sprintf("stage=L4_SOLUTION&page=%d&pageSize=%d", pg, size))
				if out.Total != l4 {
					t.Fatal("total changed", out.Total)
				}
				want := size
				if pg == pages {
					want = l4 - (pages-1)*size
				} else if pg > pages {
					want = 0
				}
				if len(out.Items) != want {
					t.Fatalf("size %d page %d: %d items want %d", size, pg, len(out.Items), want)
				}
				for _, p := range out.Items {
					if seen[p.ID] || p.ID <= last {
						t.Fatalf("size %d page %d: duplicate or unordered %s", size, pg, p.ID)
					}
					seen[p.ID] = true
					last = p.ID
				}
			}
			if len(seen) != l4 {
				t.Fatal("missing", size, len(seen))
			}
		}
	})

	t.Run("data-quality-unclassified-stage-is-reported", func(t *testing.T) {
		f := g1FixtureFor(t)
		p := createStaged(t, f.sales, "合成历史阶段", "L1_REQUIREMENT", "江苏省")
		createStaged(t, f.sales, "合成正常阶段", "L2_TECHNICAL", "江苏省")
		// A legacy/invalid stored value (synthetic). It is counted in total and
		// in unclassified, never silently dropped or rewritten.
		g1Exec(t, f.db, `UPDATE intake_projects SET sales_stage='LEGACY_STAGE_X' WHERE id=$1`, p.ID)
		sum := assertStageEquivalence(t, "legacy", f.manager, "")
		if sum.Total != 2 || sum.Unclassified != 1 {
			t.Fatal("unclassified", sum.Total, sum.Unclassified)
		}
		var stored string
		_ = f.db.QueryRow(ctx, `SELECT sales_stage FROM intake_projects WHERE id=$1`, p.ID).Scan(&stored)
		if stored != "LEGACY_STAGE_X" {
			t.Fatal("rewrote legacy data")
		}
	})

	t.Run("stage-transition-moves-counts-and-keeps-rules", func(t *testing.T) {
		f := g1FixtureFor(t)
		p := g1Submit(t, f.sales, createStaged(t, f.sales, "合成推进", "L1_REQUIREMENT", "江苏省"))
		count := func(code string) int {
			for _, sc := range stageSummaryOf(t, f.manager, "").Stages {
				if sc.Code == code {
					return sc.Count
				}
			}
			return -1
		}
		post := func(c testClient, version int, stage, reason string) (int, []byte) {
			return c.call("POST", "/api/v1/projects/"+p.ID+"/stage-change", map[string]any{"version": version, "salesStage": stage, "reason": reason})
		}
		if count("L1_REQUIREMENT") != 1 || count("L2_TECHNICAL") != 0 {
			t.Fatal("before")
		}
		// Rejections: skip ahead, missing reason, non-owner reviewer, stale version.
		for _, c := range []struct {
			client  testClient
			version int
			stage   string
			reason  string
			status  int
		}{
			{f.sales, p.Version, "L4_SOLUTION", "合成跳跃", 409},
			{f.sales, p.Version, "L2_TECHNICAL", "", 400},
			{f.manager, p.Version, "L2_TECHNICAL", "合成主管", 403},
			{f.director, p.Version, "L2_TECHNICAL", "合成总监", 403},
			{f.sales, p.Version - 1, "L2_TECHNICAL", "合成旧版本", 409},
			{f.sales, p.Version, "WON", "合成直接成交", 409},
		} {
			s, b := post(c.client, c.version, c.stage, c.reason)
			expect(t, s, c.status, b)
		}
		var events int
		_ = f.db.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE project_id=$1 AND action='project.stage-change'`, p.ID).Scan(&events)
		if events != 0 || count("L1_REQUIREMENT") != 1 {
			t.Fatal("rejected change wrote data", events)
		}
		s, b := post(f.sales, p.Version, "L2_TECHNICAL", "合成推进到技术交流")
		moved := projectFrom(t, s, b)
		if moved.SalesStage != "L2_TECHNICAL" || moved.IntakeStatus != "SUBMITTED" {
			t.Fatal("stage change touched intake status", moved.SalesStage, moved.IntakeStatus)
		}
		if count("L1_REQUIREMENT") != 0 || count("L2_TECHNICAL") != 1 {
			t.Fatal("counts did not move")
		}
		// The same request again with the old version: 409, no second change.
		s, b = post(f.sales, p.Version, "L2_TECHNICAL", "合成重复提交")
		expect(t, s, 409, b)
		var details string
		_ = f.db.QueryRow(ctx, `SELECT count(*)::text||':'||max(details::text) FROM audit_events WHERE project_id=$1 AND action='project.stage-change'`, p.ID).Scan(&details)
		if !strings.HasPrefix(details, "1:") || !strings.Contains(details, `"toStage": "L2_TECHNICAL"`) || !strings.Contains(details, `"fromStage": "L1_REQUIREMENT"`) {
			t.Fatal("audit", details)
		}
		// HOLD then only back to L1; terminal stages cannot move.
		s, b = post(f.sales, moved.Version, "HOLD", "合成暂缓")
		held := projectFrom(t, s, b)
		s, b = post(f.sales, held.Version, "L3_SITE_SURVEY", "合成越级恢复")
		expect(t, s, 409, b)
		s, b = post(f.sales, held.Version, "L1_REQUIREMENT", "合成恢复")
		resumed := projectFrom(t, s, b)
		s, b = post(f.sales, resumed.Version, "LOST", "合成失单")
		lost := projectFrom(t, s, b)
		s, b = post(f.sales, lost.Version, "L1_REQUIREMENT", "合成复活")
		expect(t, s, 409, b)
		if count("LOST") != 1 || count("HOLD") != 0 || count("L2_TECHNICAL") != 0 {
			t.Fatal("terminal counts")
		}
	})

	t.Run("constant-query-count", func(t *testing.T) {
		f := g1FixtureFor(t)
		var n atomic.Int64
		cfg := f.db.Config().Copy()
		cfg.ConnConfig.Tracer = queryCounter{&n}
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		app := &App{db: pool}
		users := g32cUsers(t, f.db)
		counts := map[int]int64{}
		for _, rows := range []int{0, 500} {
			if rows > 0 {
				g1Exec(t, f.db, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,intake_status,sales_stage,document,evidence_state,protection_basis)
 SELECT '合成计数'||g,'synthetic count '||g,'synthetic-a','synthetic-a','synthetic-a','11111111-1111-4111-8111-111111111111','DRAFT',(ARRAY['L0_LEAD','L5_QUOTATION','WON','HOLD'])[1+g%4],
 jsonb_build_object('projectName','合成计数'||g),'INCOMPLETE','LEGACY_CREATED_AT' FROM generate_series(1,$1::int) g`, rows)
			}
			n.Store(0)
			if _, err = app.stageSummary(ctx, users["manager"], map[string]string{"province": "合成省"}); err != nil {
				t.Fatal(err)
			}
			counts[rows] = n.Load()
		}
		t.Logf("STAGE_SUMMARY_QUERIES rows0=%d rows500=%d", counts[0], counts[500])
		if counts[0] != 1 || counts[500] != 1 {
			t.Fatal("stage summary must be one statement", counts)
		}
	})
}
