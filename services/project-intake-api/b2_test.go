package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func b2Database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("BAOBEI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOT_RUN: test DB required")
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" {
		t.Fatal("test DB must be loopback")
	}
	ctx := context.Background()
	admin, e := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if e != nil {
		t.Fatal(e)
	}
	schema := "baobei_b2_" + hash(randomToken())[:16]
	if _, e = admin.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Close()
		_, e := admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		if e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	if e = migrate(ctx, db); e != nil {
		t.Fatal(e)
	}
	if e = seed(ctx, db); e != nil {
		t.Fatal(e)
	}
	return db
}
func TestDatabaseB2(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	a, e := newApp(ctx, db, Config{Env: "development", Dev: true})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	a.cfg.Origin = server.URL
	ca, cb, cc := login(t, server, "synthetic-a"), login(t, server, "synthetic-b"), login(t, server, "synthetic-c")
	mgr, other, director := login(t, server, "synthetic-manager"), login(t, server, "synthetic-manager-other"), login(t, server, "synthetic-director")
	create := func(c testClient, name, customer string) Project {
		in := sample(name)
		in.Customer = customer
		s, b := c.call("POST", "/api/v1/projects", in)
		return projectFrom(t, s, b)
	}
	submit := func(c testClient, p Project) Project {
		s, b := c.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
		return projectFrom(t, s, b)
	}
	decision := func(c testClient, p Project, action string, extra map[string]any) (int, []byte) {
		body := map[string]any{"version": p.Version, "reason": "Synthetic主管核验"}
		if action == "duplicate-decisions" {
			body["relatedVersions"] = relatedVersionsFor(c, p)
		}
		for k, v := range extra {
			body[k] = v
		}
		return c.call("POST", "/api/v1/projects/"+p.ID+"/"+action, body)
	}
	t.Run("review-boundaries-director-invalid-state", func(t *testing.T) {
		p := submit(cc, create(cc, "Synthetic跨团队审批", "B2 cross"))
		s, b := decision(mgr, p, "approve", nil)
		expect(t, s, 404, b)
		s, b = decision(ca, p, "approve", nil)
		expect(t, s, 403, b)
		s, b = decision(director, p, "approve", nil)
		p = projectFrom(t, s, b)
		if p.IntakeStatus != "APPROVED" {
			t.Fatal(p)
		}
		s, b = decision(director, p, "reject", nil)
		expect(t, s, 409, b)
		q := submit(ca, create(ca, "Synthetic退回", "B2 return"))
		s, b = decision(mgr, q, "return", nil)
		q = projectFrom(t, s, b)
		if q.IntakeStatus != "RETURNED" {
			t.Fatal(q.IntakeStatus)
		}
		s, b = decision(mgr, q, "approve", nil)
		expect(t, s, 409, b)
		q = submit(ca, q)
		s, b = decision(mgr, q, "reject", nil)
		q = projectFrom(t, s, b)
		if q.IntakeStatus != "REJECTED" {
			t.Fatal(q.IntakeStatus)
		}
	})
	t.Run("returned-duplicate-resubmission-rechecks", func(t *testing.T) {
		in := sample("Synthetic退回重查")
		in.Customer = "B2 resubmit customer"
		s, b := ca.call("POST", "/api/v1/projects", in)
		base := projectFrom(t, s, b)
		_ = submit(ca, base)
		s, b = cb.call("POST", "/api/v1/projects", in)
		p := submit(cb, projectFrom(t, s, b))
		s, b = decision(mgr, p, "return", nil)
		p = projectFrom(t, s, b)
		p.Customer = "B2 changed unique customer"
		p.ProjectName = "Synthetic完全不同项目"
		p.Input.Version = p.Version
		s, b = cb.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		p = projectFrom(t, s, b)
		p = submit(cb, p)
		s, b = decision(mgr, p, "approve", nil)
		expect(t, s, 200, b)
	})
	t.Run("ownership-audit-created-by-immutable", func(t *testing.T) {
		p := submit(ca, create(ca, "Synthetic归属", "B2 transfer"))
		s, b := decision(mgr, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-c"})
		expect(t, s, 400, b)
		s, b = decision(ca, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
		expect(t, s, 403, b)
		s, b = decision(mgr, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
		p = projectFrom(t, s, b)
		if p.OwnerSubject != "synthetic-b" || p.CreatedBySubject != "synthetic-a" {
			t.Fatal(p)
		}
		s, b = ca.call("GET", "/api/v1/projects/"+p.ID, nil)
		expect(t, s, 404, b)
		s, b = cb.call("GET", "/api/v1/projects/"+p.ID, nil)
		p = projectFrom(t, s, b)
		if len(p.GovernanceHistory) < 2 {
			t.Fatal("missing ownership history")
		}
		s, b = decision(director, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-c"})
		p = projectFrom(t, s, b)
		s, b = other.call("GET", "/api/v1/projects/"+p.ID, nil)
		expect(t, s, 200, b)
		var actor string
		if e = db.QueryRow(ctx, `SELECT actor_subject FROM ownership_history WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`, p.ID).Scan(&actor); e != nil || actor != "synthetic-director" {
			t.Fatal(actor, e)
		}
	})
	t.Run("duplicate-decisions-and-cross-team-redaction", func(t *testing.T) {
		for i, action := range []string{"CONFIRMED_DUPLICATE", "DISTINCT_APPROVED"} {
			customer := fmt.Sprintf("B2 duplicate %d", i)
			_ = submit(ca, create(ca, "Synthetic重复裁决", customer))
			p := submit(cb, create(cb, "Synthetic重复裁决", customer))
			s, b := mgr.call("GET", "/api/v1/projects/"+p.ID+"/duplicate-comparison", nil)
			expect(t, s, 200, b)
			var out struct {
				Cases []Comparison `json:"cases"`
			}
			_ = json.Unmarshal(b, &out)
			if len(out.Cases) != 1 || out.Cases[0].Candidate == nil || len(out.Cases[0].Signals) == 0 {
				t.Fatal(string(b))
			}
			s, b = decision(mgr, p, "approve", nil)
			expect(t, s, 409, b)
			s, b = decision(mgr, p, "duplicate-decisions", map[string]any{"caseId": out.Cases[0].ID, "decision": action})
			p = projectFrom(t, s, b)
			want := "REJECTED"
			if i == 1 {
				want = "APPROVED"
			}
			if p.IntakeStatus != want {
				t.Fatal(p.IntakeStatus)
			}
			var stored string
			if e = db.QueryRow(ctx, `SELECT decision FROM duplicate_cases WHERE id=$1`, out.Cases[0].ID).Scan(&stored); e != nil || stored != action {
				t.Fatal(stored, e)
			}
		}
		_ = submit(cc, create(cc, "Synthetic跨组重复", "B2 duplicate cross"))
		p := submit(ca, create(ca, "Synthetic跨组重复", "B2 duplicate cross"))
		s, b := mgr.call("GET", "/api/v1/projects/"+p.ID+"/duplicate-comparison", nil)
		expect(t, s, 200, b)
		var out struct {
			Cases []Comparison `json:"cases"`
		}
		_ = json.Unmarshal(b, &out)
		if len(out.Cases) != 1 || !out.Cases[0].Restricted || out.Cases[0].Candidate != nil || strings.Contains(string(b), "synthetic-c") {
			t.Fatal("cross team disclosure", string(b))
		}
		s, b = decision(mgr, p, "duplicate-decisions", map[string]any{"caseId": out.Cases[0].ID, "decision": "DISTINCT_APPROVED"})
		expect(t, s, 403, b)
		s, b = decision(director, p, "duplicate-decisions", map[string]any{"caseId": out.Cases[0].ID, "decision": "DISTINCT_APPROVED"})
		expect(t, s, 200, b)
	})
	t.Run("protection-idempotency-no-transfer-and-policy", func(t *testing.T) {
		p := submit(ca, create(ca, "Synthetic保护期", "B2 protection"))
		_, e = db.Exec(ctx, `UPDATE intake_projects SET created_at=now()-interval '91 days',protection_started_at=now()-interval '91 days',last_activity_at=NULL WHERE id=$1`, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		now := time.Now().UTC()
		if _, e = a.protectionSweep(ctx, now); e != nil {
			t.Fatal(e)
		}
		p, e = get(ctx, db, p.ID, false)
		if e != nil {
			t.Fatal(e)
		}
		if p.ProtectionStatus != "EXPIRED" || p.OwnerSubject != "synthetic-a" || p.CreatedBySubject != "synthetic-a" || p.IntakeStatus != "SUBMITTED" || p.NextReviewAt == nil {
			t.Fatal(p)
		}
		var before int
		_ = db.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE project_id=$1`, p.ID).Scan(&before)
		revision := p.Version
		if _, e = a.protectionSweep(ctx, now); e != nil {
			t.Fatal(e)
		}
		p, _ = get(ctx, db, p.ID, false)
		var after int
		_ = db.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE project_id=$1`, p.ID).Scan(&after)
		if before != after || p.Version != revision {
			t.Fatal("not idempotent")
		}
		act := Activity{Version: p.Version, Type: "PHONE", OccurredAt: now, Summary: "Synthetic重新跟进"}
		s, b := ca.call("POST", "/api/v1/projects/"+p.ID+"/activities", act)
		p = projectFrom(t, s, b)
		if p.ProtectionStatus != "ACTIVE" {
			t.Fatal("activity did not restore protection")
		}
	})
	t.Run("stage-transition-occ-and-audit", func(t *testing.T) {
		p := submit(ca, create(ca, "Synthetic阶段", "B2 stage"))
		s, b := decision(ca, p, "stage-change", map[string]any{"salesStage": "L5_QUOTATION"})
		expect(t, s, 409, b)
		old := p
		s, b = decision(ca, p, "stage-change", map[string]any{"salesStage": "L2_TECHNICAL"})
		p = projectFrom(t, s, b)
		s, b = decision(ca, old, "stage-change", map[string]any{"salesStage": "L2_TECHNICAL"})
		expect(t, s, 409, b)
		s, b = decision(ca, p, "stage-change", map[string]any{"salesStage": "HOLD"})
		p = projectFrom(t, s, b)
		s, b = decision(ca, p, "stage-change", map[string]any{"salesStage": "L1_REQUIREMENT"})
		p = projectFrom(t, s, b)
		s, b = decision(ca, p, "stage-change", map[string]any{"salesStage": "WON"})
		expect(t, s, 409, b)
	})
	t.Run("pending-capacity-cleanup-session-expiry", func(t *testing.T) {
		_, e = db.Exec(ctx, `INSERT INTO oidc_pending_logins SELECT md5(i::text),'synthetic','synthetic','synthetic',now()+interval '1 minute',now() FROM generate_series(1,1024) i`)
		if e != nil {
			t.Fatal(e)
		}
		if e = a.storePending(ctx, hash("new"), pendingLogin{"n", "p", "b", time.Now().Add(time.Minute)}); e != errAuthCapacity {
			t.Fatal(e)
		}
		_, e = db.Exec(ctx, `UPDATE oidc_pending_logins SET expires_at=now()-interval '1 second';INSERT INTO app_sessions VALUES('synthetic-expired','synthetic-a','synthetic',now()-interval '1 second')`)
		if e != nil {
			t.Fatal(e)
		}
		if e = a.cleanupAuth(ctx); e != nil {
			t.Fatal(e)
		}
		var n int
		_ = db.QueryRow(ctx, `SELECT count(*) FROM oidc_pending_logins`).Scan(&n)
		if n != 0 {
			t.Fatal(n)
		}
		_ = db.QueryRow(ctx, `SELECT count(*) FROM app_sessions WHERE token_hash='synthetic-expired'`).Scan(&n)
		if n != 0 {
			t.Fatal(n)
		}
	})
	t.Run("concurrency-proof-global-lock-retained", func(t *testing.T) {
		run := func(inputs []Input, pair bool) {
			projects := make([]Project, len(inputs))
			for i, in := range inputs {
				s, b := ca.call("POST", "/api/v1/projects", in)
				projects[i] = projectFrom(t, s, b)
			}
			results := make(chan string, len(projects))
			var wg sync.WaitGroup
			started := time.Now()
			for _, p := range projects {
				wg.Add(1)
				go func(p Project) {
					defer wg.Done()
					s, b := ca.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
					var out Project
					_ = json.Unmarshal(b, &out)
					results <- fmt.Sprintf("%d:%s", s, out.IntakeStatus)
				}(p)
			}
			wg.Wait()
			close(results)
			counts := map[string]int{}
			for v := range results {
				counts[v]++
			}
			want := len(inputs)
			if pair {
				want /= 2
			}
			if counts["200:SUBMITTED"] != want || counts["200:UNDER_REVIEW"] != len(inputs)-want {
				t.Fatal(counts)
			}
			t.Logf("requests=%d duration=%s outcomes=%v", len(inputs), time.Since(started), counts)
		}
		unrelated := []Input{}
		for i := 0; i < 20; i++ {
			p := sample(fmt.Sprintf("Synthetic无关%d", i))
			p.Customer = fmt.Sprintf("Unrelated %d", i)
			unrelated = append(unrelated, p)
		}
		run(unrelated, false)
		pairs := []Input{}
		for i := 0; i < 10; i++ {
			p := sample("Synthetic并发相同项目")
			p.Customer = fmt.Sprintf("Paired %d", i)
			pairs = append(pairs, p, p)
		}
		run(pairs, true)
		p := sample("Synthetic模糊重复一期")
		p.Customer = "Fuzzy identical customer"
		q := p
		q.ProjectName = "Synthetic模糊重复一期工程"
		run([]Input{p, q}, true)
		p = sample("Synthetic招标A")
		p.Customer = "Tender customer A"
		p.TenderNo = "SYNTHETIC-TENDER-B2"
		q = p
		q.Customer = "Tender customer B"
		q.ProjectName = "不同fingerprint招标B"
		run([]Input{p, q}, true)
	})
}
func TestProtectionBoundaries(t *testing.T) {
	policy := ProtectionPolicy{30, 60, 90}
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		days int
		want string
	}{{29, "ACTIVE"}, {30, "WARNING"}, {60, "EXPIRING"}, {90, "EXPIRED"}} {
		got, _, _ := protection(policy, anchor, anchor.Add(time.Duration(tc.days)*24*time.Hour))
		if got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
func BenchmarkDuplicateScore(b *testing.B) {
	p := sample("Synthetic benchmark")
	for b.Loop() {
		_ = score(p, p, 1, true)
	}
}
