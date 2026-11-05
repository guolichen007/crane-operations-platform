package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// G2-3/G2-4: server-side list windows and workspace summary over real PostgreSQL.
func TestDatabaseG2Summary(t *testing.T) {
	ctx := context.Background()
	f := g1FixtureFor(t)
	listTotal := func(c testClient, query string) (int, time.Time) {
		t.Helper()
		s, b := c.call("GET", "/api/v1/projects?"+query, nil)
		expect(t, s, 200, b)
		var out struct {
			Total int       `json:"total"`
			AsOf  time.Time `json:"asOf"`
		}
		if err := json.Unmarshal(b, &out); err != nil || out.AsOf.IsZero() {
			t.Fatal("list asOf", string(b))
		}
		return out.Total, out.AsOf
	}
	summary := func(c testClient) WorkspaceSummary {
		t.Helper()
		s, b := c.call("GET", "/api/v1/workspace-summary", nil)
		expect(t, s, 200, b)
		var out WorkspaceSummary
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("empty-scope-has-zero-counts-not-errors", func(t *testing.T) {
		for _, c := range []testClient{f.sales, f.manager, f.director, f.admin} {
			s := summary(c)
			if s.Counts["total"] != 0 || s.Counts["reviewable"] != 0 || len(s.Recent) != 0 || s.AsOf.IsZero() {
				t.Fatal(s)
			}
		}
	})

	// 1,050 synthetic projects inserted directly (bulk) plus HTTP-created ones.
	g1Exec(t, f.db, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,intake_status,document,evidence_state,protection_basis,next_followup_at)
 SELECT '合成批量'||g, 'synthetic bulk '||g, CASE WHEN g%3=0 THEN 'synthetic-c' ELSE 'synthetic-a' END, 'synthetic-a','synthetic-a',
 CASE WHEN g%3=0 THEN '22222222-2222-4222-8222-222222222222'::uuid ELSE '11111111-1111-4111-8111-111111111111'::uuid END,
 'DRAFT', jsonb_build_object('projectName','合成批量'||g,'customer','合成客户'||g), 'INCOMPLETE','LEGACY_CREATED_AT',
 CASE WHEN g%5=0 THEN now()-interval '1 day' WHEN g%5=1 THEN now()+interval '3 day' WHEN g%5=2 THEN now()+interval '10 day' END
 FROM generate_series(1,1050) g`)
	unassigned := g1Create(t, f.admin, "合成首页待分配")
	submitted := g1Submit(t, f.other, g1Create(t, f.other, "合成首页待审"))

	t.Run("summary-matches-list-totals-for-every-role", func(t *testing.T) {
		for name, c := range map[string]testClient{"sales": f.sales, "manager": f.manager, "director": f.director, "admin": f.admin} {
			s := summary(c)
			total, _ := listTotal(c, "pageSize=1")
			overdue, _ := listTotal(c, "followup=overdue&pageSize=1")
			upcoming, _ := listTotal(c, "followup=upcoming&pageSize=1")
			drafts, _ := listTotal(c, "status=DRAFT&pageSize=1")
			if s.Counts["total"] != total || s.Counts["overdue"] != overdue || s.Counts["upcoming7d"] != upcoming || s.Counts["drafts"] != drafts {
				t.Fatalf("%s summary %v list total=%d overdue=%d upcoming=%d drafts=%d", name, s.Counts, total, overdue, upcoming, drafts)
			}
			t.Logf("%s SUMMARY total=%d overdue=%d upcoming7d=%d drafts=%d unassigned=%d reviewQueue=%d reviewable=%d", name, total, overdue, upcoming, drafts, s.Counts["unassignedDrafts"], s.Counts["reviewQueue"], s.Counts["reviewable"])
		}
		// Exact expected values derived from the generator, beyond page size 100.
		var a, c int
		for g := 1; g <= 1050; g++ {
			if g%3 == 0 {
				c++
			} else {
				a++
			}
		}
		if s := summary(f.sales); s.Counts["total"] != a {
			t.Fatal("sales own total", s.Counts["total"], a)
		}
		if s := summary(f.admin); s.Counts["total"] != 1050+2 || s.Counts["unassignedDrafts"] != 1 {
			t.Fatal("admin global", s.Counts)
		}
		if s := summary(f.director); s.Counts["total"] != 1050+1 || s.Counts["unassignedDrafts"] != 0 {
			t.Fatal("director excludes unassigned", s.Counts)
		}
		if s := summary(f.manager); s.Counts["total"] != a+1 {
			t.Fatal("manager team", s.Counts["total"], a+1)
		}
		_ = c
	})

	t.Run("overdue-and-upcoming-do-not-overlap", func(t *testing.T) {
		all, _ := listTotal(f.admin, "pageSize=1")
		over, _ := listTotal(f.admin, "followup=overdue&pageSize=1")
		up, _ := listTotal(f.admin, "followup=upcoming&pageSize=1")
		var later, none int
		_ = f.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE next_followup_at>=now()+interval '7 day'),count(*) FILTER (WHERE next_followup_at IS NULL) FROM intake_projects`).Scan(&later, &none)
		if over+up+later+none != all || over != 210 || up != 210 {
			t.Fatal(over, up, later, none, all)
		}
	})

	t.Run("reviewable-counts-only-independent-decisions", func(t *testing.T) {
		if s := summary(f.manager); s.Counts["reviewQueue"] != 1 || s.Counts["reviewable"] != 1 {
			t.Fatal("manager", s.Counts)
		}
		if s := summary(f.sales); s.Counts["reviewQueue"] != 0 || s.Counts["reviewable"] != 0 {
			t.Fatal("sales has no review queue", s.Counts)
		}
		// admin submits for someone else's project → participant → not reviewable.
		p := g1Create(t, f.sales, "合成首页管理员提交")
		p = g1Submit(t, f.admin, p)
		s := summary(f.admin)
		if s.Counts["reviewQueue"] != 2 || s.Counts["reviewable"] != 1 {
			t.Fatal("admin reviewable excludes own participation", s.Counts)
		}
		// summary agrees with per-project hints for every queued project.
		b := mustGetList(t, f.admin, "review=pending&pageSize=100")
		allowed := 0
		for _, item := range b {
			if item.Review != nil && item.Review.Eligible {
				allowed++
			}
		}
		if allowed != s.Counts["reviewable"] {
			t.Fatal("hint/summary mismatch", allowed, s.Counts["reviewable"])
		}
		_ = submitted
		_ = unassigned
	})

	t.Run("summary-recent-uses-display-names-and-scope", func(t *testing.T) {
		s := summary(f.sales)
		for _, r := range s.Recent {
			if r.OwnerName != "示例销售 A" {
				t.Fatal("foreign or nameless recent item", r)
			}
		}
		if len(s.Followup) == 0 || len(s.Followup) > 5 {
			t.Fatal("followups", len(s.Followup))
		}
		for _, r := range s.Followup {
			if r.NextFollowupAt == nil || !r.NextFollowupAt.Before(s.AsOf.Add(7*24*time.Hour)) {
				t.Fatal("followup outside window", r)
			}
		}
	})
	_ = fmt.Sprint
}

func mustGetList(t *testing.T, c testClient, query string) []Project {
	t.Helper()
	s, b := c.call("GET", "/api/v1/projects?"+query, nil)
	expect(t, s, 200, b)
	var out struct{ Items []Project }
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Items
}
