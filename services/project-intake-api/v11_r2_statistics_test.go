package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// V1.1 R2: layered statistics (AC-01..05). For every allowed selection the
// statistics, the stage summary and the project list agree on the same set of
// project ids; any other selection is refused by the server.

func overviewOf(t *testing.T, c testClient, query string) (int, AnalyticsOverview, []byte) {
	t.Helper()
	s, b := c.call("GET", "/api/v1/analytics/overview?"+query, nil)
	var o AnalyticsOverview
	if s == 200 {
		if err := json.Unmarshal(b, &o); err != nil {
			t.Fatal(err)
		}
	}
	return s, o, b
}

func idsOf(t *testing.T, c testClient, query string) []string {
	t.Helper()
	out := []string{}
	for page := 1; ; page++ {
		l := listOf(t, c, query+"&pageSize=100&page="+fmt.Sprint(page))
		for _, p := range l.Items {
			out = append(out, p.ID)
		}
		if page*100 >= l.Total {
			sort.Strings(out)
			return out
		}
	}
}

func dbIDs(t *testing.T, f g1Fixture, where string, args ...any) []string {
	t.Helper()
	rows, err := f.db.Query(context.Background(), `SELECT id::text FROM intake_projects p WHERE p.discarded_at IS NULL AND `+where+` ORDER BY 1`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func sameIDs(t *testing.T, label string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: got %d ids, want %d", label, len(got), len(want))
	}
}

func TestDatabaseV11LayeredStatistics(t *testing.T) {
	f := g1FixtureFor(t)
	ma, d1, a1, a2 := f.manager, f.director, f.sales, f.other
	mb, c1 := login(t, f.server, "synthetic-manager-other"), login(t, f.server, "synthetic-c")
	const teamA, teamB = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	amount := func(c testClient, name string, v *float64, stage string) Project {
		in := sample(name)
		in.Customer = name + "客户"
		in.SalesStage = stage
		in.EstimatedAmount = v
		s, b := c.call("POST", "/api/v1/projects", in)
		return projectFrom(t, s, b)
	}
	// Fixtures: known, unknown amounts in several stages for each owner.
	for i, c := range []testClient{a1, a1, a1, a2, a2, ma, ma, c1, mb} {
		var v *float64
		if i%2 == 0 {
			v = pointer(float64(1000*(i+1)) + 0.25)
		}
		p := amount(c, fmt.Sprintf("V11统计%d", i), v, stages[i%len(stages)])
		if i%3 == 0 {
			g1Submit(t, c, p)
		}
	}
	maID, a1ID, a2ID, c1ID := staffIDOf(t, f.db, "synthetic-manager"), staffIDOf(t, f.db, "synthetic-a"), staffIDOf(t, f.db, "synthetic-b"), staffIDOf(t, f.db, "synthetic-c")
	directorID := staffIDOf(t, f.db, "synthetic-director")

	check := func(label string, c testClient, query string, want []string) AnalyticsOverview {
		t.Helper()
		s, o, b := overviewOf(t, c, query)
		expect(t, s, 200, b)
		sameIDs(t, label+" list", idsOf(t, c, query), want)
		if o.Total != len(want) {
			t.Fatalf("%s: overview %d want %d", label, o.Total, len(want))
		}
		sum := stageSummaryOf(t, c, query)
		if sum.Total != o.Total || sum.Amount != o.Amount {
			t.Fatalf("%s: stage-summary %d/%+v overview %d/%+v", label, sum.Total, sum.Amount, o.Total, o.Amount)
		}
		groups := 0
		for _, g := range o.Groups {
			groups += g.Count
			gl := listOf(t, c, query+"&group="+g.Code+"&pageSize=1").Total
			if gl != g.Count {
				t.Fatalf("%s group %s: list %d overview %d", label, g.Code, gl, g.Count)
			}
		}
		if groups+o.Unclassified != o.Total {
			t.Fatal(label, "groups do not add up")
		}
		m := o.Amount
		if m.KnownCount+m.UnknownCount+m.LegacyUnconfirmedCount+m.InvalidCount != o.Total {
			t.Fatal(label, "amount classes do not add up")
		}
		return o
	}
	// AC-01 sales: own projects only (default and mine).
	check("A1 default", a1, "", dbIDs(t, f, "owner_subject='synthetic-a'"))
	check("A1 mine", a1, "scope=mine", dbIDs(t, f, "owner_subject='synthetic-a'"))
	// AC-03 supervisor mine, AC-04 per member and team = union.
	mine := check("MA mine", ma, "scope=mine", dbIDs(t, f, "owner_subject='synthetic-manager'"))
	if mine.Selection.Scope != "mine" || strings.Join(mine.AvailableScopes, ",") != "team,mine,member" {
		t.Fatalf("selection %+v %v", mine.Selection, mine.AvailableScopes)
	}
	check("MA member A1", ma, "scope=member&memberId="+a1ID, dbIDs(t, f, "owner_subject='synthetic-a'"))
	check("MA member A2", ma, "scope=member&memberId="+a2ID, dbIDs(t, f, "owner_subject='synthetic-b'"))
	check("MA member self", ma, "scope=member&memberId="+maID, dbIDs(t, f, "owner_subject='synthetic-manager'"))
	team := check("MA team", ma, "scope=team", dbIDs(t, f, "owner_subject IS NOT NULL AND team_id=$1", teamA))
	parts := len(dbIDs(t, f, "owner_subject IN ('synthetic-a','synthetic-b','synthetic-manager','synthetic-staff-desktop','synthetic-staff-mobile')"))
	if team.Total != parts {
		t.Fatal("team total is not the union of members", team.Total, parts)
	}
	// Known amount of the team equals the sum of members (exact decimal text).
	var mr MembersReport
	s, b := ma.call("GET", "/api/v1/analytics/members", nil)
	expect(t, s, 200, b)
	_ = json.Unmarshal(b, &mr)
	sumRows := 0
	for _, m := range mr.Members {
		sumRows += m.Total
		if m.MemberID == c1ID {
			t.Fatal("other-team member listed")
		}
	}
	if sumRows != team.Total || mr.Total != team.Total || mr.Members[0].Role != managerRole {
		t.Fatal("member rows", sumRows, mr.Total, team.Total)
	}
	var memberKnown, teamKnown string
	_ = f.db.QueryRow(context.Background(), `SELECT round(sum(estimated_amount),2)::text FROM intake_projects WHERE team_id=$1 AND owner_subject IS NOT NULL AND estimated_amount IS NOT NULL`, teamA).Scan(&teamKnown)
	memberKnown = team.Amount.KnownTotal
	if memberKnown != teamKnown {
		t.Fatal("team known amount", memberKnown, teamKnown)
	}

	// AC-02/AC-05: refusals. Nothing about the other side is returned.
	for _, tc := range []struct {
		c     testClient
		query string
		code  int
	}{
		{a1, "scope=member&memberId=" + a2ID, 403},
		{a1, "scope=team", 403},
		{a1, "scope=all", 403},
		{ma, "scope=member&memberId=" + c1ID, 404},
		{ma, "scope=member&memberId=" + directorID, 404},
		{ma, "scope=team&teamId=" + teamB, 404},
		{ma, "scope=all", 403},
		{mb, "scope=member&memberId=" + a1ID, 404},
		{mb, "scope=member&memberId=" + maID, 404},
		{ma, "scope=member&memberId=00000000-0000-4000-8000-000000000000", 404},
		{ma, "memberId=" + a1ID, 400},
		{ma, "scope=mine&memberId=" + a1ID, 400},
		{ma, "scope=everything", 400},
	} {
		for _, path := range []string{"/api/v1/analytics/overview?", "/api/v1/stage-summary?", "/api/v1/projects?"} {
			s, b := tc.c.call("GET", path+tc.query, nil)
			if s != tc.code {
				t.Fatalf("%s%s: %d want %d %s", path, tc.query, s, tc.code, b)
			}
			for _, leak := range []string{"示例销售", "示例主管", "示例另一组", "V11统计", "示例总监"} {
				if strings.Contains(string(b), leak) {
					t.Fatalf("%s%s leaks %q: %s", path, tc.query, leak, b)
				}
			}
		}
	}
	// A refused member and a nonexistent member give the same answer.
	_, b1 := mb.call("GET", "/api/v1/analytics/overview?scope=member&memberId="+a1ID, nil)
	_, b2 := mb.call("GET", "/api/v1/analytics/overview?scope=member&memberId=00000000-0000-4000-8000-000000000000", nil)
	strip := func(b []byte) string { return strings.Split(string(b), `"requestId"`)[0] }
	if strip(b1) != strip(b2) {
		t.Fatal("existence oracle", string(b1), string(b2))
	}
	s, b = a1.call("GET", "/api/v1/analytics/members", nil)
	expect(t, s, 403, b)
	s, b = ma.call("GET", "/api/v1/analytics/members?teamId="+teamB, nil)
	expect(t, s, 404, b)

	// Director: global, per team, per member (any team).
	check("D1 all", d1, "scope=all", dbIDs(t, f, "owner_subject IS NOT NULL"))
	check("D1 team B", d1, "scope=team&teamId="+teamB, dbIDs(t, f, "owner_subject IS NOT NULL AND team_id=$1", teamB))
	check("D1 member C1", d1, "scope=member&memberId="+c1ID, dbIDs(t, f, "owner_subject='synthetic-c'"))
	s, b = d1.call("GET", "/api/v1/analytics/overview?scope=mine", nil)
	expect(t, s, 403, b)
	s, b = d1.call("GET", "/api/v1/analytics/members", nil)
	expect(t, s, 200, b)
	var dr MembersReport
	_ = json.Unmarshal(b, &dr)
	if len(dr.Teams) < 2 || dr.Total != len(dbIDs(t, f, "owner_subject IS NOT NULL")) {
		t.Fatal("director team rows", dr.Total)
	}

	// AC-12: a transfer moves the current project to the new owner at once,
	// with no double count; creation/submission history stays with A1.
	moved := g1Create(t, a1, "V11移交统计")
	beforeA1, beforeA2 := len(idsOf(t, ma, "scope=member&memberId="+a1ID)), len(idsOf(t, ma, "scope=member&memberId="+a2ID))
	s, b = g1Decision(ma, v11Get(t, ma, moved.ID), "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
	expect(t, s, 200, b)
	if len(idsOf(t, ma, "scope=member&memberId="+a1ID)) != beforeA1-1 || len(idsOf(t, ma, "scope=member&memberId="+a2ID)) != beforeA2+1 {
		t.Fatal("transfer not reflected exactly once")
	}
	if p := v11Get(t, a2, moved.ID); p.CreatedBySubject != "synthetic-a" {
		t.Fatal("creator rewritten")
	}
	// A supervisor moved to another team: their projects (team B now) are no
	// longer team A's, and team A's supervisor cannot select them.
	g1Exec(t, f.db, `UPDATE app_users SET team_id=$1 WHERE subject='synthetic-staff-mobile'`, teamB)
	s, b = ma.call("GET", "/api/v1/analytics/overview?scope=member&memberId="+staffIDOf(t, f.db, "synthetic-staff-mobile"), nil)
	expect(t, s, 404, b)
}

// 1000 projects (500 in L0) and 20 concurrent readers. Reports P95; the
// 800 ms target is checked, the measured values are logged.
func TestDatabaseV11StatisticsScale(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	const teamA = "11111111-1111-4111-8111-111111111111"
	if _, err := f.db.Exec(ctx, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,sales_stage,estimated_amount,document,next_followup_at)
 SELECT 'V11规模'||g,'v11规模'||g,(ARRAY['synthetic-a','synthetic-b','synthetic-manager'])[1+g%3],'synthetic-a','synthetic-a',$1,
  CASE WHEN g<=500 THEN 'L0_LEAD' ELSE (ARRAY['L1_REQUIREMENT','L2_TECHNICAL','L3_SITE_SURVEY','L4_SOLUTION','L5_QUOTATION','L6_TENDER','L7_COMMERCIAL','L8_PENDING_CONTRACT','WON','LOST','HOLD'])[1+g%11] END,
  CASE WHEN g%2=0 THEN g*10 END,
  jsonb_build_object('projectName','V11规模'||g,'customer','规模客户'||g,'estimatedAmount',CASE WHEN g%2=0 THEN to_jsonb(g*10) ELSE 'null'::jsonb END),
  now()+(g%20-10)*interval '1 day'
 FROM generate_series(1,1000) g`, teamA); err != nil {
		t.Fatal(err)
	}
	maID := staffIDOf(t, f.db, "synthetic-a")
	queries := []struct {
		c testClient
		q string
	}{
		{f.manager, "/api/v1/analytics/overview?scope=team"},
		{f.manager, "/api/v1/analytics/overview?scope=member&memberId=" + maID},
		{f.manager, "/api/v1/analytics/members"},
		{f.manager, "/api/v1/projects?scope=team&group=G1&pageSize=10"},
		{f.director, "/api/v1/analytics/overview?scope=all"},
		{f.director, "/api/v1/stage-summary"},
		{f.sales, "/api/v1/analytics/overview?scope=mine"},
	}
	var mu sync.Mutex
	durations := map[string][]time.Duration{}
	var wg sync.WaitGroup
	for w := 0; w < 20; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				q := queries[(w+i)%len(queries)]
				start := time.Now()
				s, b := q.c.call("GET", q.q, nil)
				d := time.Since(start)
				if s != 200 {
					t.Error(q.q, s, string(b))
					return
				}
				u, _ := url.Parse(q.q)
				mu.Lock()
				durations[u.Path] = append(durations[u.Path], d)
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	all := []time.Duration{}
	for path, ds := range durations {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		t.Logf("V11_SCALE %s n=%d p50=%v p95=%v", path, len(ds), ds[len(ds)/2], ds[len(ds)*95/100])
		all = append(all, ds...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	p95 := all[len(all)*95/100]
	t.Logf("V11_SCALE overall n=%d p95=%v (target 800ms, 1000 projects, 20 concurrent)", len(all), p95)
	if p95 > 800*time.Millisecond {
		t.Errorf("P95 %v above 800ms target", p95)
	}
	// Correctness at scale: 500 in L0, group G1 = L0+L1, list total matches.
	o := AnalyticsOverview{}
	s, b := f.director.call("GET", "/api/v1/analytics/overview?scope=all", nil)
	expect(t, s, 200, b)
	_ = json.Unmarshal(b, &o)
	l0 := 0
	for _, st := range o.Stages {
		if st.Code == "L0_LEAD" {
			l0 = st.Count
		}
	}
	if l0 < 500 || o.Groups[0].Count != listOf(t, f.director, "group=G1&pageSize=1").Total {
		t.Fatal("scale counts", l0, o.Groups[0].Count)
	}
}
