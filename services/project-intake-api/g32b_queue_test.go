package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// G3-2B review queue consistency. Everything runs over HTTP against an
// isolated real PostgreSQL schema with synthetic identities.

type queuePage struct {
	Items  []Project      `json:"items"`
	Total  int            `json:"total"`
	AsOf   time.Time      `json:"asOf"`
	Counts map[string]int `json:"counts"`
}

func queue(t *testing.T, c testClient, eligibility string, page, size int) (queuePage, []byte) {
	t.Helper()
	s, b := c.call("GET", fmt.Sprintf("/api/v1/reviews?kind=pending&eligibility=%s&page=%d&pageSize=%d", eligibility, page, size), nil)
	expect(t, s, 200, b)
	var q queuePage
	if err := json.Unmarshal(b, &q); err != nil {
		t.Fatal(err)
	}
	return q, b
}
func summaryCounts(t *testing.T, c testClient) map[string]int {
	t.Helper()
	s, b := c.call("GET", "/api/v1/workspace-summary", nil)
	expect(t, s, 200, b)
	var w WorkspaceSummary
	_ = json.Unmarshal(b, &w)
	return w.Counts
}
func transfer(t *testing.T, c testClient, p Project, target string) Project {
	t.Helper()
	s, b := c.call("GET", "/api/v1/projects/"+p.ID, nil)
	p = projectFrom(t, s, b)
	s, b = g1Decision(c, p, "ownership-transfer", map[string]any{"targetSubject": target})
	return projectFrom(t, s, b)
}
func fresh(t *testing.T, c testClient, p Project) Project {
	t.Helper()
	s, b := c.call("GET", "/api/v1/projects/"+p.ID, nil)
	return projectFrom(t, s, b)
}

func TestDatabaseG32BReviewQueue(t *testing.T) {
	ctx := context.Background()

	t.Run("ten-item-matrix-four-roles", func(t *testing.T) {
		f := g1FixtureFor(t)
		hiddenSales := login(t, f.server, "synthetic-c")
		otherManager := login(t, f.server, "synthetic-manager-other")
		named := map[string]Project{}
		create := func(c testClient, name string) Project { return g1Create(t, c, "合成队列"+name) }
		// 3 reviewable for the manager.
		for _, n := range []string{"可审一", "可审二", "可审三"} {
			named[n] = g1Submit(t, f.sales, create(f.sales, n))
		}
		// 4 where the manager participated (ownership hand-offs are participation).
		p := transfer(t, f.manager, create(f.sales, "参与一"), "synthetic-b")
		named["参与一"] = g1Submit(t, f.other, p)
		p = g1Submit(t, f.sales, create(f.sales, "参与二"))
		named["参与二"] = transfer(t, f.manager, p, "synthetic-b")
		p = transfer(t, f.manager, create(f.sales, "参与三"), "synthetic-b")
		p = transfer(t, f.manager, p, "synthetic-a")
		named["参与三"] = g1Submit(t, f.sales, p)
		p = transfer(t, f.manager, create(f.sales, "参与四"), "synthetic-b")
		p = transfer(t, f.director, p, "synthetic-a")
		named["参与四"] = g1Submit(t, f.sales, p)
		// 2 with a related candidate in a team the manager cannot read.
		hiddenIDs := []string{}
		for _, n := range []string{"跨组一", "跨组二"} {
			h := g1Submit(t, hiddenSales, create(hiddenSales, n))
			hiddenIDs = append(hiddenIDs, h.ID, h.RegistrationNo)
			named[n] = g1Submit(t, f.sales, create(f.sales, n))
			if named[n].IntakeStatus != "UNDER_REVIEW" {
				t.Fatal("cross-team candidate not detected", n)
			}
		}
		// 1 whose current submission evidence is incomplete (migrated history).
		named["证据不完整"] = g1Submit(t, f.sales, create(f.sales, "证据不完整"))
		g1Exec(t, f.db, `UPDATE intake_projects SET evidence_state='INCOMPLETE' WHERE id=$1`, named["证据不完整"].ID)

		want := map[string]map[string]int{
			"manager":       {"reviewable": 3, "blocked": 7, "all": 10},
			"director":      {"reviewable": 10, "blocked": 2, "all": 12},
			"admin":         {"reviewable": 11, "blocked": 1, "all": 12},
			"other-manager": {"reviewable": 2, "blocked": 0, "all": 2},
		}
		clients := map[string]testClient{"manager": f.manager, "director": f.director, "admin": f.admin, "other-manager": otherManager}
		blockedCodes := map[string]string{}
		for who, c := range clients {
			home := summaryCounts(t, c)
			ids := map[string]map[string]bool{}
			for _, e := range []string{"reviewable", "blocked", "all"} {
				q, raw := queue(t, c, e, 1, 100)
				t.Logf("QUEUE %s %s total=%d counts=%v", who, e, q.Total, q.Counts)
				if q.Total != want[who][e] || len(q.Items) != q.Total {
					t.Fatalf("%s eligibility=%s total=%d items=%d want %d", who, e, q.Total, len(q.Items), want[who][e])
				}
				for k, v := range want[who] {
					if q.Counts[k] != v {
						t.Fatalf("%s counts[%s]=%d want %d", who, k, q.Counts[k], v)
					}
				}
				if q.AsOf.IsZero() {
					t.Fatal("asOf missing")
				}
				ids[e] = map[string]bool{}
				for _, it := range q.Items {
					ids[e][it.ID] = true
					if it.Review == nil {
						t.Fatal("queue item without review hints", it.RegistrationNo)
					}
					next := "approve"
					if it.Review.NextAction == "duplicate-decisions" {
						next = "duplicate-decisions"
					}
					// Membership equals the same hint the detail page and POST use.
					if (e == "reviewable") != it.Actions[next].Allowed && e != "all" {
						t.Fatalf("%s %s %s membership disagrees with action hint %+v", who, e, it.RegistrationNo, it.Actions[next])
					}
					if e == "blocked" {
						if it.Review.ReasonCode == nil || *it.Review.ReasonCode == "" {
							t.Fatal("blocked item without reason", it.RegistrationNo)
						}
						if who == "manager" {
							blockedCodes[strings.TrimPrefix(it.ProjectName, "合成队列")] = *it.Review.ReasonCode
						}
					}
					if it.SubmittedAt == nil {
						t.Fatal("queue item without trusted submission time", it.RegistrationNo)
					}
				}
				if who == "manager" {
					for _, secret := range hiddenIDs {
						if strings.Contains(string(raw), secret) {
							t.Fatalf("manager queue leaks hidden candidate %q", secret)
						}
					}
				}
			}
			if len(ids["reviewable"])+len(ids["blocked"]) != len(ids["all"]) {
				t.Fatal("reviewable+blocked must partition all", who)
			}
			for id := range ids["reviewable"] {
				if ids["blocked"][id] || !ids["all"][id] {
					t.Fatal("partition broken", who, id)
				}
			}
			if home["reviewable"] != want[who]["reviewable"] || home["reviewQueue"] != want[who]["all"] {
				t.Fatalf("%s home reviewable=%d reviewQueue=%d, queue %v", who, home["reviewable"], home["reviewQueue"], want[who])
			}
		}
		wantCodes := map[string]string{"参与一": "REVIEW_CONFLICT", "参与二": "REVIEW_CONFLICT", "参与三": "REVIEW_CONFLICT", "参与四": "REVIEW_CONFLICT",
			"跨组一": "REVIEW_RELATED_PROJECT_RESTRICTED", "跨组二": "REVIEW_RELATED_PROJECT_RESTRICTED", "证据不完整": "REVIEW_EVIDENCE_INCOMPLETE"}
		for n, code := range wantCodes {
			if blockedCodes[n] != code {
				t.Fatalf("manager blocked reason %s=%q want %q (all %v)", n, blockedCodes[n], code, blockedCodes)
			}
		}
		// Sales cannot read the review queue in any form.
		for _, e := range []string{"", "reviewable", "blocked", "all"} {
			s, b := f.sales.call("GET", "/api/v1/reviews?kind=pending&eligibility="+e, nil)
			expect(t, s, 403, b)
		}
		// Real POSTs agree with the listed hints (same data state).
		for n := range wantCodes {
			s, b := g1Decision(f.manager, fresh(t, f.manager, named[n]), "approve", nil)
			if s == 200 {
				t.Fatalf("blocked item %s was approved", n)
			}
			_ = b
		}
		for _, n := range []string{"可审一", "可审二", "可审三"} {
			s, b := g1Decision(f.manager, fresh(t, f.manager, named[n]), "approve", nil)
			if projectFrom(t, s, b).IntakeStatus != "APPROVED" {
				t.Fatal(n)
			}
		}
		q, _ := queue(t, f.manager, "reviewable", 1, 20)
		if q.Total != 0 || q.Counts["all"] != 7 || summaryCounts(t, f.manager)["reviewable"] != 0 {
			t.Fatal("after approvals", q.Total, q.Counts)
		}
	})

	t.Run("parameters-and-legacy-default", func(t *testing.T) {
		f := g1FixtureFor(t)
		g1Submit(t, f.sales, g1Create(t, f.sales, "合成参数"))
		// Without eligibility the old response shape and semantics are kept.
		s, b := f.manager.call("GET", "/api/v1/reviews?kind=pending", nil)
		expect(t, s, 200, b)
		var legacy map[string]json.RawMessage
		_ = json.Unmarshal(b, &legacy)
		if _, ok := legacy["counts"]; ok || string(legacy["total"]) != "1" {
			t.Fatal("legacy default changed", string(b))
		}
		for _, bad := range []string{
			"kind=duplicates&eligibility=reviewable", "kind=protection&eligibility=all",
			"kind=pending&eligibility=mine", "kind=pending&eligibility=all&pageSize=101",
			"kind=pending&eligibility=all&pageSize=0", "kind=pending&eligibility=all&page=0",
		} {
			s, b = f.manager.call("GET", "/api/v1/reviews?"+bad, nil)
			expect(t, s, 400, b)
		}
		// eligibility without kind means the pending queue.
		s, b = f.manager.call("GET", "/api/v1/reviews?eligibility=all", nil)
		expect(t, s, 200, b)
	})

	t.Run("pagination-1001-stable-ties", func(t *testing.T) {
		f := g1FixtureFor(t)
		seed := g1Submit(t, f.sales, g1Create(t, f.sales, "合成分页"))
		// Clone the frozen submission 1000 times: identical submission times force ties.
		g1Exec(t, f.db, `INSERT INTO intake_projects SELECT (jsonb_populate_record(NULL::intake_projects, to_jsonb(p) || jsonb_build_object(
 'id', gen_random_uuid(), 'registration_no', 'BB-2099-'||lpad(g::text,6,'0'), 'project_name', p.project_name||g, 'normalized_project_name', p.normalized_project_name||g))).*
 FROM intake_projects p, generate_series(1,1000) g WHERE p.id=$1`, seed.ID)
		g1Exec(t, f.db, `INSERT INTO project_submissions SELECT (jsonb_populate_record(NULL::project_submissions, to_jsonb(s) || jsonb_build_object('project_id', c.id))).*
 FROM project_submissions s JOIN intake_projects c ON c.registration_no LIKE 'BB-2099-%' WHERE s.project_id=$1`, seed.ID)
		var dbIDs int
		_ = f.db.QueryRow(ctx, `SELECT count(*) FROM intake_projects WHERE intake_status='SUBMITTED' AND team_id::text=$1`, "11111111-1111-4111-8111-111111111111").Scan(&dbIDs)
		if dbIDs != 1001 {
			t.Fatal("fixture", dbIDs)
		}
		for _, c := range []struct {
			e          string
			size, last int
		}{{"reviewable", 100, 1}, {"all", 20, 1}, {"all", 9, 1001 % 9}} {
			seen := map[string]bool{}
			pages := (1001 + c.size - 1) / c.size
			var prev *time.Time
			for pg := 1; pg <= pages+1; pg++ {
				q, _ := queue(t, f.manager, c.e, pg, c.size)
				if q.Total != 1001 {
					t.Fatal("total", q.Total)
				}
				switch {
				case pg == pages && len(q.Items) != c.last:
					t.Fatalf("last page %d has %d items want %d", pg, len(q.Items), c.last)
				case pg > pages && len(q.Items) != 0:
					t.Fatal("page after the end must be empty", len(q.Items))
				case pg < pages && len(q.Items) != c.size:
					t.Fatal("short page", pg, len(q.Items))
				}
				for _, it := range q.Items {
					if seen[it.ID] {
						t.Fatal("duplicate across pages", it.ID)
					}
					seen[it.ID] = true
					if prev != nil && it.SubmittedAt.Before(*prev) {
						t.Fatal("order broken")
					}
					prev = it.SubmittedAt
				}
			}
			if len(seen) != 1001 {
				t.Fatalf("%s/%d saw %d of 1001", c.e, c.size, len(seen))
			}
		}
		if summaryCounts(t, f.manager)["reviewable"] != 1001 {
			t.Fatal("home count")
		}
	})

	t.Run("stale-list-post-rejected", func(t *testing.T) {
		f := g1FixtureFor(t)
		p := g1Submit(t, f.sales, g1Create(t, f.sales, "合成并发"))
		q, _ := queue(t, f.manager, "reviewable", 1, 20)
		if q.Total != 1 || !q.Items[0].Actions["approve"].Allowed {
			t.Fatal("setup", q.Total)
		}
		listed := q.Items[0]
		// Another reviewer decides first.
		s, b := g1Decision(f.director, fresh(t, f.director, p), "return", nil)
		projectFrom(t, s, b)
		// The cached button state must not be trusted.
		s, b = g1Decision(f.manager, listed, "approve", nil)
		expect(t, s, 409, b)
		if !strings.Contains(string(b), "VERSION_CONFLICT") {
			t.Fatal(string(b))
		}
		q, _ = queue(t, f.manager, "all", 1, 20)
		if q.Total != 0 {
			t.Fatal("refreshed queue still lists the returned project")
		}
	})

	t.Run("constant-query-count", func(t *testing.T) {
		f := g1FixtureFor(t)
		for i := 0; i < 60; i++ {
			c := []testClient{f.sales, f.other}[i%2]
			g1Submit(t, c, g1Create(t, c, fmt.Sprintf("合成队列负载%03d", i)))
		}
		counts := map[int]int64{}
		for _, size := range []int{5, 50} {
			var n atomic.Int64
			cfg := f.db.Config().Copy()
			cfg.BeforeAcquire = func(context.Context, *pgx.Conn) bool { return true }
			cfg.ConnConfig.Tracer = queryCounter{&n}
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			app := &App{db: pool, cfg: f.app.cfg}
			var u User
			if err = pool.QueryRow(ctx, `SELECT subject,role,coalesce(team_id::text,'') FROM app_users WHERE subject='synthetic-manager'`).Scan(&u.Subject, &u.Role, &u.TeamID); err != nil {
				t.Fatal(err)
			}
			n.Store(0)
			start := time.Now()
			out, err := app.reviewQueue(ctx, u, "all", 1, size)
			elapsed := time.Since(start)
			pool.Close()
			if err != nil || len(out.Items) != size || out.Counts["all"] != 60 {
				t.Fatal(err, len(out.Items), out.Counts)
			}
			counts[size] = n.Load()
			t.Logf("QUEUE_PERF pageSize=%d queries=%d elapsed=%s", size, counts[size], elapsed)
		}
		if counts[5] != counts[50] || counts[50] > 8 {
			t.Fatal("queue hints are not constant-query", counts)
		}
	})
}
