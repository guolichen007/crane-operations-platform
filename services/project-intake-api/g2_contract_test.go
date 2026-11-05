package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// G2-0 contract closure. Every case runs over HTTP against a real, isolated
// PostgreSQL schema with synthetic signed-in identities.
func TestDatabaseG2Contract(t *testing.T) {
	ctx := context.Background()

	t.Run("legacy-incomplete-candidate-does-not-deadlock-new-filing", func(t *testing.T) {
		f := g1FixtureFor(t)
		legacy := g1Submit(t, f.sales, g1Create(t, f.sales, "合成历史候选"))
		// Reproduce a migrated 00001-00003 row: approved, no frozen submission.
		g1Exec(t, f.db, `ALTER TABLE intake_projects DROP CONSTRAINT submission_projection`)
		g1Exec(t, f.db, `UPDATE intake_projects SET intake_status='APPROVED',evidence_state='INCOMPLETE',submission_revision=0,submitted_by_subject=NULL WHERE id=$1`, legacy.ID)
		for _, decision := range []string{"DISTINCT_APPROVED", "CONFIRMED_DUPLICATE"} {
			fresh := g1Submit(t, f.other, g1Create(t, f.other, "合成历史候选"))
			if fresh.IntakeStatus != "UNDER_REVIEW" {
				t.Fatal("candidate not detected", fresh.IntakeStatus)
			}
			s, b := f.director.call("GET", "/api/v1/projects/"+fresh.ID, nil)
			detail := projectFrom(t, s, b)
			if detail.Review == nil || !detail.Review.Eligible || !detail.Actions["duplicate-decisions"].Allowed {
				t.Fatalf("director hint should allow decision: %+v %+v", detail.Review, detail.Actions["duplicate-decisions"])
			}
			cases := comparisonCases(t, f.director, fresh)
			s, b = g1Decision(f.director, fresh, "duplicate-decisions", map[string]any{"caseId": cases[0].ID, "decision": decision})
			out := projectFrom(t, s, b)
			want := map[string]string{"DISTINCT_APPROVED": "APPROVED", "CONFIRMED_DUPLICATE": "REJECTED"}[decision]
			if out.IntakeStatus != want {
				t.Fatal(decision, out.IntakeStatus)
			}
		}
		// The original sales owner of the legacy candidate is still not independent:
		// promote synthetic-a to manager temporarily and verify the conflict.
		fresh := g1Submit(t, f.other, g1Create(t, f.other, "合成历史候选"))
		g1Exec(t, f.db, `UPDATE app_users SET role='baobei_director',team_id=NULL WHERE subject='synthetic-a'`)
		formerOwner := login(t, f.server, "synthetic-a")
		s, b := formerOwner.call("GET", "/api/v1/projects/"+fresh.ID, nil)
		detail := projectFrom(t, s, b)
		if detail.Actions["duplicate-decisions"].Allowed || code(detail.Actions["duplicate-decisions"]) != "REVIEW_CONFLICT" {
			t.Fatal("legacy owner hint", detail.Actions["duplicate-decisions"])
		}
		cases := comparisonCases(t, formerOwner, fresh)
		s, b = g1Decision(formerOwner, fresh, "duplicate-decisions", map[string]any{"caseId": cases[0].ID, "decision": "DISTINCT_APPROVED"})
		expect(t, s, 403, b)
		if !strings.Contains(string(b), "REVIEW_CONFLICT") {
			t.Fatal(string(b))
		}
		// The primary filing itself still requires complete evidence.
		g1Exec(t, f.db, `UPDATE intake_projects SET evidence_state='INCOMPLETE' WHERE id=$1`, fresh.ID)
		s, b = g1Decision(f.director, fresh, "return", nil)
		expect(t, s, 409, b)
		if !strings.Contains(string(b), "REVIEW_EVIDENCE_INCOMPLETE") {
			t.Fatal(string(b))
		}
	})

	t.Run("review-eligibility-hints-match-transaction", func(t *testing.T) {
		f := g1FixtureFor(t)
		// admin edits then submits: admin is a participant and cannot review.
		p := g1Create(t, f.sales, "合成资格提示")
		p.Input.Version = p.Version
		s, b := f.admin.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		p = g1Submit(t, f.admin, projectFrom(t, s, b))
		for _, c := range []struct {
			client  testClient
			allowed bool
			code    string
		}{{f.admin, false, "REVIEW_CONFLICT"}, {f.manager, true, ""}, {f.director, true, ""}} {
			s, b = c.client.call("GET", "/api/v1/projects/"+p.ID, nil)
			d := projectFrom(t, s, b)
			if d.Review == nil || d.Review.Eligible != c.allowed || d.Actions["approve"].Allowed != c.allowed || code(d.Actions["approve"]) != c.code {
				t.Fatalf("hint mismatch %+v %+v", d.Review, d.Actions["approve"])
			}
			if !d.Review.ProjectReadable {
				t.Fatal("readable flag")
			}
		}
		s, b = g1Decision(f.admin, p, "approve", nil)
		expect(t, s, 403, b)
		// list hints agree with detail hints.
		s, b = f.manager.call("GET", "/api/v1/projects?q="+p.RegistrationNo, nil)
		expect(t, s, 200, b)
		var list struct{ Items []Project }
		_ = json.Unmarshal(b, &list)
		if len(list.Items) != 1 || !list.Items[0].Actions["approve"].Allowed {
			t.Fatal("list hint", string(b))
		}
		s, b = g1Decision(f.manager, p, "approve", nil)
		if projectFrom(t, s, b).IntakeStatus != "APPROVED" {
			t.Fatal("independent manager approval")
		}
		// cross-team candidate is restricted for a manager: hint and POST agree.
		base := g1Submit(t, login(t, f.server, "synthetic-c"), g1Create(t, login(t, f.server, "synthetic-c"), "合成跨组提示"))
		_ = base
		q := g1Submit(t, f.other, g1Create(t, f.other, "合成跨组提示"))
		s, b = f.manager.call("GET", "/api/v1/projects/"+q.ID, nil)
		d := projectFrom(t, s, b)
		if d.Actions["duplicate-decisions"].Allowed || code(d.Actions["duplicate-decisions"]) != "REVIEW_RELATED_PROJECT_RESTRICTED" || d.Review.RelatedCandidates.Allowed {
			t.Fatal("restricted hint", d.Actions["duplicate-decisions"], d.Review)
		}
		if code(d.Actions["approve"]) != "DUPLICATE_DECISION_REQUIRED" || d.Review.PendingDuplicates != 1 {
			t.Fatal("pending hint", d.Actions["approve"])
		}
	})

	t.Run("admin-batch-handoff-scope-version-participation", func(t *testing.T) {
		f := g1FixtureFor(t)
		var fromID, toID, crossID, managerID string
		row := `SELECT staff_id::text FROM app_users WHERE subject=$1`
		for subject, dst := range map[string]*string{"synthetic-a": &fromID, "synthetic-b": &toID, "synthetic-c": &crossID, "synthetic-manager": &managerID} {
			if err := f.db.QueryRow(ctx, row, subject).Scan(dst); err != nil {
				t.Fatal(err)
			}
		}
		p := g1Submit(t, f.sales, g1Create(t, f.sales, "合成管理员交接"))
		q := g1Create(t, f.sales, "合成管理员交接草稿")
		s, b := f.admin.call("GET", "/api/v1/staff/"+fromID, nil)
		expect(t, s, 200, b)
		var detail struct {
			Member   staffMember    `json:"member"`
			Projects []staffProject `json:"projects"`
		}
		_ = json.Unmarshal(b, &detail)
		if len(detail.Projects) != 2 {
			t.Fatal("admin handoff list", string(b))
		}
		before := countRows(t, f.db, `SELECT count(*) FROM project_participations WHERE actor_subject='synthetic-admin' AND kind='ASSIGN'`)
		body := map[string]any{"version": detail.Member.Version, "targetId": crossID, "reason": "管理员跨组交接", "projects": []map[string]any{{"id": p.ID, "version": p.Version}, {"id": q.ID, "version": q.Version + 9}}}
		s, b = f.admin.call("POST", "/api/v1/staff/"+fromID+"/ownership-transfer-batch", body)
		expect(t, s, 200, b)
		if !strings.Contains(string(b), "TRANSFERRED") || !strings.Contains(string(b), "VERSION_CONFLICT") {
			t.Fatal(string(b))
		}
		var owner, team, creator string
		if err := f.db.QueryRow(ctx, `SELECT owner_subject,team_id::text,created_by_subject FROM intake_projects WHERE id=$1`, p.ID).Scan(&owner, &team, &creator); err != nil || owner != "synthetic-c" || creator != "synthetic-a" || team != otherSyntheticTeam {
			t.Fatal(owner, team, creator, err)
		}
		if after := countRows(t, f.db, `SELECT count(*) FROM project_participations WHERE actor_subject='synthetic-admin' AND kind='ASSIGN'`); after != before+1 {
			t.Fatal("missing participation evidence", before, after)
		}
		// The admin who handed the project off is now a participant and cannot review it.
		s, b = f.admin.call("GET", "/api/v1/projects/"+p.ID, nil)
		if d := projectFrom(t, s, b); d.Actions["approve"].Allowed {
			t.Fatal("handoff actor may not review")
		}
		// Non-sales source and sales actor remain denied.
		s, b = f.admin.call("POST", "/api/v1/staff/"+managerID+"/ownership-transfer-batch", body)
		expect(t, s, 403, b)
		s, b = f.sales.call("POST", "/api/v1/staff/"+toID+"/ownership-transfer-batch", body)
		expect(t, s, 403, b)
		// manager() still means manager/director only.
		if manager(User{Role: adminRole}) {
			t.Fatal("manager semantics widened")
		}
	})

	t.Run("create-receipt-recovery-actor-bound", func(t *testing.T) {
		f := g1FixtureFor(t)
		key := "g2-receipt-" + strings.Repeat("a", 20)
		in := sample("合成回执恢复")
		s, b := f.admin.keyed(key, map[string]any{"projectName": in.ProjectName, "customer": "合成客户", "version": 1})
		created := projectFrom(t, s, b)
		if created.OwnerSubject != "" || created.AssignmentStatus != "UNASSIGNED_DRAFT" {
			t.Fatal("unassigned draft")
		}
		s, b = f.admin.call("GET", "/api/v1/project-create-receipts/"+key, nil)
		expect(t, s, 200, b)
		var receipt struct {
			Project        Project `json:"project"`
			CreatedVersion int     `json:"createdVersion"`
		}
		if json.Unmarshal(b, &receipt) != nil || receipt.Project.ID != created.ID || receipt.CreatedVersion != 1 || !receipt.Project.Replayed {
			t.Fatal(string(b))
		}
		// Later edits are reflected: receipt returns the current version.
		created.Input.Version = created.Version
		created.Notes = "回执后编辑"
		s, b = f.admin.call("PATCH", "/api/v1/projects/"+created.ID, created.Input)
		edited := projectFrom(t, s, b)
		s, b = f.admin.call("GET", "/api/v1/project-create-receipts/"+key, nil)
		_ = json.Unmarshal(b, &receipt)
		if receipt.Project.Version != edited.Version || receipt.Project.Notes != "回执后编辑" {
			t.Fatal("receipt not current")
		}
		// Another account with the same key learns nothing; malformed key is 400.
		for _, c := range []testClient{f.sales, f.director} {
			s, b = c.call("GET", "/api/v1/project-create-receipts/"+key, nil)
			expect(t, s, 404, b)
			if strings.Contains(string(b), created.ID) {
				t.Fatal("receipt leak")
			}
		}
		s, b = f.admin.call("GET", "/api/v1/project-create-receipts/short", nil)
		expect(t, s, 400, b)
		// A sales creator loses the receipt once the project leaves their scope.
		salesKey := "g2-receipt-" + strings.Repeat("b", 20)
		s, b = f.sales.keyed(salesKey, map[string]any{"projectName": "合成销售回执", "version": 1})
		sp := projectFrom(t, s, b)
		s, b = g1Decision(f.manager, sp, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
		projectFrom(t, s, b)
		s, b = f.sales.call("GET", "/api/v1/project-create-receipts/"+salesKey, nil)
		expect(t, s, 404, b)
		// A logged-out session cannot query receipts.
		s, b = f.admin.call("POST", "/auth/logout", map[string]any{})
		s, b = f.admin.call("GET", "/api/v1/project-create-receipts/"+key, nil)
		expect(t, s, 401, b)
	})

	t.Run("display-names-without-private-identity", func(t *testing.T) {
		f := g1FixtureFor(t)
		u := g1Create(t, f.admin, "合成显示名")
		s, b := f.admin.call("GET", "/api/v1/projects/"+u.ID, nil)
		d := projectFrom(t, s, b)
		if d.OwnerName != "" || d.CreatedByName != "示例管理员" || d.CustodianName != "示例管理员" {
			t.Fatalf("names %+v", d)
		}
		s, b = g1Decision(f.admin, d, "ownership-transfer", map[string]any{"targetSubject": "synthetic-a"})
		d = projectFrom(t, s, b)
		s, b = f.sales.call("GET", "/api/v1/projects/"+d.ID, nil)
		d = projectFrom(t, s, b)
		if d.OwnerName != "示例销售 A" || len(d.GovernanceHistory) == 0 || d.GovernanceHistory[0].ToName != "示例销售 A" || d.GovernanceHistory[0].ActorName != "示例管理员" || d.Audit[0].ActorName == "" {
			t.Fatalf("names %+v %+v", d.GovernanceHistory, d.Audit)
		}
		for _, secret := range []string{"@example.test", "staffId", "workEmail"} {
			if strings.Contains(string(b), secret) {
				t.Fatal("private identity leaked", secret)
			}
		}
		var raw map[string]any
		_ = json.Unmarshal(b, &raw)
		if raw["ownerName"] != "示例销售 A" {
			t.Fatal(raw["ownerName"])
		}
	})

	t.Run("list-hints-constant-query-count", func(t *testing.T) {
		f := g1FixtureFor(t)
		for i := 0; i < 60; i++ {
			c := []testClient{f.sales, f.other}[i%2]
			p := g1Create(t, c, fmt.Sprintf("合成负载%03d", i))
			p.Input.Version = p.Version
			p.Customer = fmt.Sprintf("合成负载客户%03d", i)
			s, b := c.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
			g1Submit(t, c, projectFrom(t, s, b))
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
			items, total, err := app.list(ctx, u, map[string]string{"pageSize": fmt.Sprint(size)})
			pool.Close()
			if err != nil || len(items) != size || total < 60 {
				t.Fatal(err, len(items), total)
			}
			counts[size] = n.Load()
		}
		t.Logf("LIST_QUERIES page5=%d page50=%d", counts[5], counts[50])
		if counts[5] != counts[50] || counts[50] > 6 {
			t.Fatal("list hints are not constant-query", counts)
		}
	})
}

type queryCounter struct{ n *atomic.Int64 }

func (q queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.n.Add(1)
	return ctx
}
func (queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func code(h ActionHint) string {
	if h.ReasonCode == nil {
		return ""
	}
	return *h.ReasonCode
}
func comparisonCases(t *testing.T, c testClient, p Project) []Comparison {
	t.Helper()
	s, b := c.call("GET", "/api/v1/projects/"+p.ID+"/duplicate-comparison", nil)
	expect(t, s, 200, b)
	var out struct {
		Cases []Comparison `json:"cases"`
	}
	if json.Unmarshal(b, &out) != nil || len(out.Cases) == 0 {
		t.Fatal("no comparison cases", string(b))
	}
	return out.Cases
}
func countRows(t *testing.T, db *pgxpool.Pool, sql string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
