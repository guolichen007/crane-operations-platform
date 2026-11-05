package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProjectPolicyMatrix(t *testing.T) {
	roles := []string{salesRole, managerRole, directorRole, adminRole}
	statuses := strings.Fields("DRAFT RETURNED SUBMITTED UNDER_REVIEW APPROVED REJECTED EXPIRED CANCELLED CONVERTED")
	actions := strings.Fields("edit submit activities stage-change ownership-transfer approve return reject duplicate-decisions")
	count := 0
	for _, role := range roles {
		for _, resource := range []string{"own", "same-team", "other-team", "unassigned"} {
			for _, status := range statuses {
				p := Project{OwnerSubject: "sales", TeamID: "one", CreatedBySubject: "creator", SubmittedBySubject: "submitter", EvidenceState: "COMPLETE", SubmissionRevision: 1, IntakeStatus: status}
				if resource == "same-team" {
					p.OwnerSubject = "other"
				}
				if resource == "other-team" {
					p.OwnerSubject = "other"
					p.TeamID = "two"
				}
				if resource == "unassigned" {
					p.OwnerSubject = ""
					p.TeamID = ""
				}
				u := User{Subject: "sales", Role: role, TeamID: "one", Permissions: capabilityList(7)}
				if role != salesRole {
					u.Subject = "reviewer"
				}
				visible := role == adminRole || resource != "unassigned" && (role == directorRole || role == managerRole && resource != "other-team" || role == salesRole && resource == "own")
				if canRead(u, p) != visible {
					t.Fatalf("read %s/%s/%s", role, resource, status)
				}
				for _, action := range actions {
					allowed := false
					if visible {
						switch action {
						case "edit":
							allowed = (role == adminRole || role == salesRole) && (status == "DRAFT" || status == "RETURNED")
						case "submit":
							allowed = resource != "unassigned" && (role == adminRole || role == salesRole) && (status == "DRAFT" || status == "RETURNED")
						case "activities":
							allowed = (role == adminRole || role == salesRole) && (status == "SUBMITTED" || status == "UNDER_REVIEW" || status == "APPROVED")
						case "stage-change":
							allowed = (role == adminRole || role == salesRole) && contains(statuses[:5], status)
						case "ownership-transfer":
							allowed = role != salesRole && contains(statuses[:5], status)
						default:
							allowed = role != salesRole && (status == "SUBMITTED" || status == "UNDER_REVIEW")
						}
					}
					if resource == "unassigned" && status != "DRAFT" {
						allowed = false
					}
					got := (ProjectPolicy{u}).Action(p, action) == ""
					if got != allowed {
						t.Fatalf("%s/%s/%s/%s got %v", role, resource, status, action, got)
					}
					count++
				}
			}
		}
	}
	u := User{Subject: "sales", Role: salesRole}
	p := Project{OwnerSubject: "sales", IntakeStatus: "DRAFT"}
	if (ProjectPolicy{u}).Create() || (ProjectPolicy{u}).Action(p, "submit") == "" {
		t.Fatal("sales capability bypass")
	}
	t.Logf("POLICY_MATRIX=%d action cases plus read scopes", count)
}

type g1Fixture struct {
	db                                     *pgxpool.Pool
	app                                    *App
	server                                 *httptest.Server
	admin, sales, other, manager, director testClient
}

func g1FixtureFor(t *testing.T) g1Fixture {
	db := b2Database(t)
	a := &App{db: db, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(a.handler())
	t.Cleanup(server.Close)
	a.cfg.Origin = server.URL
	return g1Fixture{db, a, server, login(t, server, "synthetic-admin"), login(t, server, "synthetic-a"), login(t, server, "synthetic-b"), login(t, server, "synthetic-manager"), login(t, server, "synthetic-director")}
}
func (c testClient) keyed(key string, body any) (int, []byte) {
	c.t.Helper()
	req, err := http.NewRequest("POST", c.origin+"/api/v1/projects", bytes.NewReader(mustJSON(body)))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin)
	req.Header.Set("X-CSRF-Token", c.csrf)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	r, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return r.StatusCode, b
}
func g1Create(t *testing.T, c testClient, name string) Project {
	in := sample(name)
	in.Customer = name
	s, b := c.call("POST", "/api/v1/projects", in)
	return projectFrom(t, s, b)
}
func g1Submit(t *testing.T, c testClient, p Project) Project {
	s, b := c.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
	return projectFrom(t, s, b)
}
func g1Decision(c testClient, p Project, action string, extra map[string]any) (int, []byte) {
	in := map[string]any{"version": p.Version, "reason": "合成独立性核验"}
	if action == "duplicate-decisions" {
		in["relatedVersions"] = relatedVersionsFor(c, p)
	}
	for k, v := range extra {
		in[k] = v
	}
	return c.call("POST", "/api/v1/projects/"+p.ID+"/"+action, in)
}
func g1Exec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func relatedVersionsFor(c testClient, p Project) map[string]int {
	out := map[string]int{}
	status, b := c.call("GET", "/api/v1/projects/"+p.ID+"/duplicate-comparison", nil)
	if status != 200 {
		return out
	}
	var body struct {
		Cases []Comparison `json:"cases"`
	}
	if json.Unmarshal(b, &body) != nil {
		return out
	}
	for _, candidate := range body.Cases {
		if candidate.Candidate != nil {
			out[candidate.Candidate.ID] = candidate.Candidate.Version
		}
	}
	return out
}
func TestDatabaseG1(t *testing.T) {
	ctx := context.Background()
	t.Run("admin-no-sales-assignment-and-independent-review", func(t *testing.T) {
		f := g1FixtureFor(t)
		g1Exec(t, f.db, `UPDATE app_users SET active=false WHERE role='baobei_sales'`)
		p := g1Create(t, f.admin, "合成无销售草稿")
		if p.OwnerSubject != "" || p.TeamID != "" || p.DraftCustodianSubject != "synthetic-admin" || p.ProtectedUntil != nil || p.ProtectionStatus != "NOT_STARTED" {
			t.Fatal("fictional owner/protection")
		}
		s, b := f.admin.call("GET", "/api/v1/projects/"+p.ID, nil)
		expect(t, s, 200, b)
		var raw map[string]any
		_ = json.Unmarshal(b, &raw)
		if raw["ownerSubject"] != nil || raw["teamId"] != nil {
			t.Fatal("owner must be JSON null")
		}
		for _, c := range []testClient{f.manager, f.director} {
			s, b = c.call("GET", "/api/v1/projects/"+p.ID, nil)
			expect(t, s, 404, b)
		}
		s, b = f.admin.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
		expect(t, s, 409, b)
		s, b = g1Decision(f.admin, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
		expect(t, s, 409, b)
		g1Exec(t, f.db, `UPDATE app_users SET active=true WHERE subject='synthetic-b'`)
		s, b = g1Decision(f.admin, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
		p = projectFrom(t, s, b)
		p = g1Submit(t, f.admin, p)
		for _, action := range []string{"approve", "return", "reject", "duplicate-decisions"} {
			s, b = g1Decision(f.admin, p, action, nil)
			expect(t, s, 403, b)
			if !bytes.Contains(b, []byte("REVIEW_CONFLICT")) {
				t.Fatal(string(b))
			}
		}
		s, b = g1Decision(f.manager, p, "approve", nil)
		p = projectFrom(t, s, b)
		if p.IntakeStatus != "APPROVED" {
			t.Fatal(p.IntakeStatus)
		}
		s, b = f.admin.call("POST", "/api/v1/projects/"+p.ID+"/activities", Activity{Version: p.Version, Type: "PHONE", OccurredAt: time.Now(), Summary: "合成管理员代录"})
		p = projectFrom(t, s, b)
		s, b = g1Decision(f.admin, p, "stage-change", map[string]any{"salesStage": "L2_TECHNICAL"})
		p = projectFrom(t, s, b)
		var actor string
		if err := f.db.QueryRow(ctx, `SELECT subject FROM project_activities WHERE project_id=$1`, p.ID).Scan(&actor); err != nil || actor != "synthetic-admin" {
			t.Fatal(actor, err)
		}
	})
	t.Run("idempotency-retries-actor-isolation-concurrent-tabs", func(t *testing.T) {
		f := g1FixtureFor(t)
		in := sample("合成幂等")
		key := randomToken()
		s, b := f.sales.keyed("", in)
		expect(t, s, 400, b)
		ids := make(chan string, 16)
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, b := f.sales.keyed(key, in)
				if s != 200 {
					ids <- fmt.Sprintf("HTTP%d:%s", s, b)
					return
				}
				var p Project
				_ = json.Unmarshal(b, &p)
				ids <- p.ID
			}()
		}
		wg.Wait()
		close(ids)
		id := ""
		for v := range ids {
			if id == "" {
				id = v
			}
			if v != id || !uuidPattern.MatchString(v) {
				t.Fatal("duplicate or failed create", v)
			}
		}
		changed := in
		changed.ProjectName += "changed"
		s, b = f.sales.keyed(key, changed)
		expect(t, s, 409, b)
		s, b = f.other.keyed(key, in)
		p := projectFrom(t, s, b)
		if p.ID == id {
			t.Fatal("cross actor receipt")
		}
		s, b = f.sales.call("GET", "/api/v1/projects/"+id, nil)
		first := projectFrom(t, s, b)
		first.Input.Notes = "后来编辑"
		first.Input.Version = first.Version
		s, b = f.sales.call("PATCH", "/api/v1/projects/"+id, first.Input)
		latest := projectFrom(t, s, b)
		s, b = f.sales.keyed(key, in)
		replay := projectFrom(t, s, b)
		if !replay.Replayed || replay.Version != latest.Version || replay.Notes != "后来编辑" {
			t.Fatal("stale replay")
		}
		var count int
		_ = f.db.QueryRow(ctx, `SELECT count(*) FROM project_create_requests WHERE actor_subject=$1 AND idempotency_key=$2`, "synthetic-a", key).Scan(&count)
		if count != 1 {
			t.Fatal(count)
		}
	})
	t.Run("creator-editor-submitter-owner-and-transfer-conflicts", func(t *testing.T) {
		f := g1FixtureFor(t)
		for _, mode := range []string{"creator", "editor", "submitter", "owner", "assignment-after-submit"} {
			t.Run(mode, func(t *testing.T) {
				p := g1Create(t, f.sales, "合成自审"+mode)
				if mode == "editor" {
					p.Input.Version = p.Version
					s, b := f.admin.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
					p = projectFrom(t, s, b)
				}
				if mode == "submitter" {
					p = g1Submit(t, f.admin, p)
				} else {
					p = g1Submit(t, f.sales, p)
				}
				actor := f.admin
				if mode == "creator" || mode == "owner" {
					g1Exec(t, f.db, `UPDATE app_users SET role='baobei_admin',team_id=NULL WHERE subject='synthetic-a'`)
					actor = login(t, f.server, "synthetic-a")
				}
				if mode == "creator" {
					s, b := g1Decision(f.director, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
					p = projectFrom(t, s, b)
				}
				if mode == "assignment-after-submit" {
					s, b := g1Decision(f.admin, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
					p = projectFrom(t, s, b)
				}
				for _, action := range []string{"approve", "return", "reject", "duplicate-decisions"} {
					s, b := g1Decision(actor, p, action, nil)
					expect(t, s, 403, b)
				}
				if mode == "creator" || mode == "owner" {
					g1Exec(t, f.db, `UPDATE app_users SET role='baobei_sales',team_id=$1 WHERE subject='synthetic-a'`, syntheticTeam)
					f.sales = login(t, f.server, "synthetic-a")
				}
			})
		}
	})
	t.Run("all-related-duplicate-evidence-and-no-partial-decision", func(t *testing.T) {
		f := g1FixtureFor(t)
		base := g1Create(t, f.sales, "合成多候选")
		base = g1Submit(t, f.admin, base) // admin only participated in a candidate.
		foreign := g1Create(t, login(t, f.server, "synthetic-c"), "合成多候选")
		_ = g1Submit(t, login(t, f.server, "synthetic-c"), foreign)
		second := g1Create(t, f.other, "合成多候选")
		second = g1Submit(t, f.other, second)
		s, b := f.manager.call("GET", "/api/v1/projects/"+second.ID+"/duplicate-comparison", nil)
		expect(t, s, 200, b)
		var comparison struct {
			Cases []Comparison `json:"cases"`
		}
		_ = json.Unmarshal(b, &comparison)
		if len(comparison.Cases) == 0 {
			t.Fatal("missing candidate")
		}
		extra := map[string]any{"caseId": comparison.Cases[0].ID, "decision": "DISTINCT_APPROVED"}
		s, b = g1Decision(f.admin, second, "duplicate-decisions", extra)
		expect(t, s, 403, b)
		var pending int
		_ = f.db.QueryRow(ctx, `SELECT count(*) FROM duplicate_cases WHERE project_id=$1 AND decision='PENDING'`, second.ID).Scan(&pending)
		if pending != len(comparison.Cases) {
			t.Fatal("partial decision")
		}
		versions := relatedVersionsFor(f.director, second)
		versions[base.ID]++
		extra["relatedVersions"] = versions
		s, b = g1Decision(f.director, second, "duplicate-decisions", extra)
		expect(t, s, 409, b)
		delete(extra, "relatedVersions")
		s, b = g1Decision(f.manager, second, "duplicate-decisions", extra)
		expect(t, s, 403, b)
		s, b = g1Decision(f.director, second, "duplicate-decisions", extra)
		second = projectFrom(t, s, b)
		// Decided evidence cannot be deleted even by the table-owning test migrator.
		if _, err := f.db.Exec(ctx, `DELETE FROM duplicate_cases WHERE project_id=$1`, second.ID); err == nil {
			t.Fatal("deleted decided evidence")
		}
		third := g1Create(t, f.other, "合成多候选")
		third = g1Submit(t, f.other, third)
		s, b = f.manager.call("GET", "/api/v1/projects/"+third.ID+"/duplicate-comparison", nil)
		expect(t, s, 200, b)
		_ = json.Unmarshal(b, &comparison)
		extra = map[string]any{"caseId": comparison.Cases[0].ID, "decision": "CONFIRMED_DUPLICATE"}
		s, b = g1Decision(f.director, third, "duplicate-decisions", extra)
		third = projectFrom(t, s, b)
		g1Exec(t, f.db, `UPDATE intake_projects SET intake_status='RETURNED' WHERE id=$1`, third.ID)
		s, b = f.other.call("POST", "/api/v1/projects/"+third.ID+"/submit", map[string]int{"version": third.Version})
		expect(t, s, 409, b)
		if _, err := f.db.Exec(ctx, `UPDATE project_submissions SET evidence_state='INCOMPLETE' WHERE project_id=$1`, base.ID); err == nil {
			t.Fatal("rewrote frozen submission")
		}
	})
	t.Run("sql-and-go-resource-scope-agree", func(t *testing.T) {
		f := g1FixtureFor(t)
		a := g1Create(t, f.sales, "合成范围A")
		b := g1Create(t, f.other, "合成范围B")
		u := g1Create(t, f.admin, "合成未分配")
		c := g1Create(t, login(t, f.server, "synthetic-c"), "合成跨团队")
		for _, subject := range []string{"synthetic-a", "synthetic-manager", "synthetic-director", "synthetic-admin"} {
			var user User
			var mask int
			err := f.db.QueryRow(ctx, `SELECT subject,role,coalesce(team_id::text,''),staff_permissions FROM app_users WHERE subject=$1`, subject).Scan(&user.Subject, &user.Role, &user.TeamID, &mask)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []Project{a, b, c, u} {
				var visible bool
				err = f.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM intake_projects p WHERE `+projectScope("p")+` AND id=$4)`, user.Subject, user.Role, user.TeamID, p.ID).Scan(&visible)
				if err != nil || visible != canRead(user, p) {
					t.Fatal(subject, p.ID, visible, err)
				}
			}
		}
	})
	t.Run("identity-lock-races-assignment-submission-review", func(t *testing.T) {
		for _, operation := range []string{"assignment-disable", "submission-disable", "review-disable", "assignment-role", "submission-role", "review-role"} {
			t.Run(operation, func(t *testing.T) {
				mode := strings.Split(operation, "-")[1]
				operation = strings.Split(operation, "-")[0]
				f := g1FixtureFor(t)
				p := g1Create(t, f.sales, "合成并发"+operation)
				actor := f.admin
				subject := "synthetic-a"
				if operation == "assignment" {
					p = g1Create(t, f.admin, "合成并发待分配")
					subject = "synthetic-b"
				}
				if operation == "review" {
					p = g1Submit(t, f.sales, p)
					actor = f.manager
					subject = "synthetic-manager"
				}
				tx, err := f.db.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
					t.Fatal(err)
				}
				done := make(chan int, 1)
				go func() {
					var s int
					if operation == "assignment" {
						s, _ = g1Decision(actor, p, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
					} else if operation == "review" {
						s, _ = g1Decision(actor, p, "approve", nil)
					} else {
						s, _ = actor.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
					}
					done <- s
				}()
				mutation := `UPDATE app_users SET active=false WHERE subject=$1`
				if mode == "role" {
					mutation = `UPDATE app_users SET role='baobei_director',team_id=NULL WHERE subject=$1`
				}
				if _, err = tx.Exec(ctx, mutation, subject); err != nil {
					t.Fatal(err)
				}
				if operation == "review" {
					if _, err = tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, subject); err != nil {
						t.Fatal(err)
					}
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case code := <-done:
					if code != 409 && code != 403 && code != 401 {
						t.Fatal("identity change bypass", code)
					}
				case <-time.After(12 * time.Second):
					t.Fatal("lock order deadlock")
				}
			})
		}
	})
}

func TestNumericKnowledgeCompatibility(t *testing.T) {
	for _, tc := range []struct{ body, state string }{
		{`{"estimatedAmount":null}`, "UNKNOWN"}, {`{"estimatedAmount":0}`, "LEGACY_UNCONFIRMED"}, {`{"estimatedAmount":0,"knowledge":{"estimatedAmount":"KNOWN"}}`, "KNOWN"},
	} {
		var in Input
		if err := json.Unmarshal([]byte(tc.body), &in); err != nil {
			t.Fatal(err)
		}
		if err := validateInput(&in, false); err != nil {
			t.Fatal(err)
		}
		if in.Knowledge["estimatedAmount"] != tc.state {
			t.Fatal(in.Knowledge)
		}
	}
	var in Input
	_ = json.Unmarshal([]byte(`{"plannedEquipment":[{"quantity":null,"capacityTons":0,"spanM":null,"liftHeightM":0,"knowledge":{"liftHeightM":"KNOWN"}}]}`), &in)
	if err := validateInput(&in, false); err != nil {
		t.Fatal(err)
	}
	e := in.PlannedEquipment[0]
	if e.Knowledge["quantity"] != "UNKNOWN" || e.Knowledge["capacityTons"] != "LEGACY_UNCONFIRMED" || e.Knowledge["liftHeightM"] != "KNOWN" {
		t.Fatal(e.Knowledge)
	}
}
