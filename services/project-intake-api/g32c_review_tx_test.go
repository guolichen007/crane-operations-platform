package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// G3-2C review transaction semantics. Every expectation below was first run
// against the pre-G3-2C implementation (8601f1b) and passed there; the
// batched transaction must keep each status, reason code and the absence of
// partial writes. The transcript (BAOBEI_G32C_TX_TRANSCRIPT) is diffed
// before/after in the acceptance report.

var g32cTxTranscript strings.Builder

func errCode(b []byte) string {
	var v struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(b, &v)
	return v.Code
}

// dbState is everything a review POST may write.
func dbState(t *testing.T, w g32cWorld) string {
	t.Helper()
	var s string
	err := w.f.db.QueryRow(context.Background(), `SELECT concat_ws('|',
 (SELECT string_agg(id::text||':'||intake_status||':'||version,',' ORDER BY id) FROM intake_projects),
 (SELECT string_agg(id::text||':'||decision||':'||coalesce(decided_by_subject,''),',' ORDER BY id) FROM duplicate_cases),
 (SELECT count(*) FROM approvals),
 (SELECT count(*) FROM audit_events),
 (SELECT count(*) FROM project_participations))`).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type txWorld struct {
	g32cWorld
	clean  []Project // readable, independent candidates (team one)
	extra  Project   // the special candidate of a scenario (zero if none)
	admin2 testClient
}

// buildTx: two clean team-one candidates, an optional special candidate made
// by prepare(), then the primary (owner synthetic-b) submitted through the API.
func buildTx(t *testing.T, name string, prepare func(f g1Fixture) Project) txWorld {
	t.Helper()
	f := g1FixtureFor(t)
	var w txWorld
	for i := 0; i < 2; i++ {
		w.clean = append(w.clean, g1Submit(t, f.sales, g1Create(t, f.sales, name)))
	}
	if prepare != nil {
		w.extra = prepare(f)
	}
	primary := g1Submit(t, f.other, g1Create(t, f.other, name))
	w.g32cWorld = g32cWorld{f: f, primary: primary, users: g32cUsers(t, f.db), clients: map[string]testClient{"manager": f.manager, "director": f.director, "admin": f.admin, "sales": f.other}}
	return w
}

// caseFor returns the PENDING case of the primary whose candidate is id.
func (w txWorld) caseFor(t *testing.T, id string) string {
	t.Helper()
	var c string
	if err := w.f.db.QueryRow(context.Background(), `SELECT id::text FROM duplicate_cases WHERE project_id=$1 AND candidate_id=$2 AND decision='PENDING'`, w.primary.ID, id).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

// allVersions are the related versions a fully informed reviewer would send
// (read straight from the database, so hidden candidates are included).
func (w txWorld) allVersions(t *testing.T) map[string]int {
	t.Helper()
	rows, err := w.f.db.Query(context.Background(), `SELECT c.id::text,c.version FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=$1`, w.primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var v int
		_ = rows.Scan(&id, &v)
		out[id] = v
	}
	return out
}

func (w txWorld) fresh(t *testing.T) Project {
	t.Helper()
	p, err := get(context.Background(), w.f.db, w.primary.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// post sends a review POST and records the outcome in the transcript.
func (w txWorld) post(t *testing.T, label string, c testClient, who, action string, extra map[string]any) (int, []byte) {
	t.Helper()
	p := w.fresh(t)
	in := map[string]any{"version": p.Version, "reason": "合成独立性核验"}
	if action == "duplicate-decisions" {
		in["relatedVersions"] = w.allVersions(t)
	}
	for k, v := range extra {
		if v == nil {
			delete(in, k)
		} else {
			in[k] = v
		}
	}
	s, b := c.call("POST", "/api/v1/projects/"+p.ID+"/"+action, in)
	decision, _ := in["decision"].(string)
	fmt.Fprintf(&g32cTxTranscript, "%s | %s | %s %s | HTTP %d %s\n", label, who, action, decision, s, errCode(b))
	return s, b
}

// reject asserts a refused POST changed nothing.
func (w txWorld) reject(t *testing.T, label string, c testClient, who, action string, extra map[string]any, status int, code string) {
	t.Helper()
	before := dbState(t, w.g32cWorld)
	s, b := w.post(t, label, c, who, action, extra)
	if s != status || errCode(b) != code {
		t.Fatalf("%s %s %s: HTTP %d %s want %d %s", label, who, action, s, b, status, code)
	}
	if after := dbState(t, w.g32cWorld); after != before {
		t.Fatalf("%s %s %s: rejected request wrote data\nbefore %s\nafter  %s", label, who, action, before, after)
	}
}

func TestDatabaseG32CReviewTransactionMatrix(t *testing.T) {
	ctx := context.Background()
	both := []string{"DISTINCT_APPROVED", "CONFIRMED_DUPLICATE"}
	// Candidate-side conflicts. The selected case is always a clean candidate,
	// so DISTINCT_APPROVED and CONFIRMED_DUPLICATE must check every related
	// candidate, not only the selected one.
	conflicts := []struct {
		label, reviewer string
		prepare         func(t *testing.T, f g1Fixture, name string) Project
		clean           string
	}{
		{"审核人曾创建候选", "admin", func(t *testing.T, f g1Fixture, name string) Project {
			in := sample(name)
			in.Customer = name
			body := map[string]any{}
			_ = json.Unmarshal(mustJSON(in), &body)
			body["assignmentMode"], body["targetSubject"] = "ASSIGN_ACTIVE_SALES", "synthetic-a"
			s, b := f.admin.call("POST", "/api/v1/projects", body)
			return g1Submit(t, f.sales, projectFrom(t, s, b))
		}, "director"},
		{"审核人曾提交候选", "admin", func(t *testing.T, f g1Fixture, name string) Project {
			return g1Submit(t, f.admin, g1Create(t, f.sales, name))
		}, "director"},
		{"审核人曾编辑候选", "admin", func(t *testing.T, f g1Fixture, name string) Project {
			p := g1Create(t, f.sales, name)
			p.Input.Version = p.Version
			s, b := f.admin.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
			return g1Submit(t, f.sales, projectFrom(t, s, b))
		}, "director"},
		{"审核人曾经手候选归属（提交前）", "director", func(t *testing.T, f g1Fixture, name string) Project {
			p := transfer(t, f.director, g1Create(t, f.sales, name), "synthetic-b")
			return g1Submit(t, f.other, p)
		}, "manager"},
		{"审核人曾经手候选归属（提交后）", "manager", func(t *testing.T, f g1Fixture, name string) Project {
			return transfer(t, f.manager, g1Submit(t, f.sales, g1Create(t, f.sales, name)), "synthetic-b")
		}, "director"},
	}
	for i, sc := range conflicts {
		t.Run(sc.label, func(t *testing.T) {
			name := fmt.Sprintf("合成事务冲突%02d", i)
			w := buildTx(t, name, func(f g1Fixture) Project { return sc.prepare(t, f, name) })
			sel := w.caseFor(t, w.clean[0].ID)
			for _, d := range both {
				w.reject(t, sc.label, w.clients[sc.reviewer], sc.reviewer, "duplicate-decisions", map[string]any{"caseId": sel, "decision": d, "acknowledgeLegacyEvidence": true}, 403, "REVIEW_CONFLICT")
			}
			// approve is still gated by pending duplicates, not by candidate conflicts.
			w.reject(t, sc.label, w.clients[sc.reviewer], sc.reviewer, "approve", nil, 409, "DUPLICATE_DECISION_REQUIRED")
			s, b := w.post(t, sc.label, w.clients[sc.clean], sc.clean, "duplicate-decisions", map[string]any{"caseId": sel, "decision": "DISTINCT_APPROVED"})
			if out := projectFrom(t, s, b); out.IntakeStatus != "APPROVED" {
				t.Fatal(out.IntakeStatus)
			}
		})
	}
	t.Run("审核人曾作为候选负责人", func(t *testing.T) {
		w := buildTx(t, "合成事务前负责人", nil)
		g1Exec(t, w.f.db, `UPDATE app_users SET role='baobei_director',team_id=NULL WHERE subject='synthetic-a'`)
		former := login(t, w.f.server, "synthetic-a")
		for _, d := range both {
			w.reject(t, "前负责人", former, "former-owner", "duplicate-decisions", map[string]any{"caseId": w.caseFor(t, w.clean[1].ID), "decision": d}, 403, "REVIEW_CONFLICT")
		}
	})
	t.Run("管理员参与主项目后自审", func(t *testing.T) {
		w := buildTx(t, "合成事务管理员自审", nil)
		// A new round: return, admin edits the primary, sales B resubmits.
		s, b := w.post(t, "管理员自审", w.f.director, "director", "return", nil)
		p := projectFrom(t, s, b)
		p.Input.Version = p.Version
		s, b = w.f.admin.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		g1Submit(t, w.f.other, projectFrom(t, s, b))
		for _, action := range []string{"approve", "return", "reject"} {
			w.reject(t, "管理员自审", w.f.admin, "admin", action, nil, 403, "REVIEW_CONFLICT")
		}
		w.reject(t, "管理员自审", w.f.admin, "admin", "duplicate-decisions", map[string]any{"caseId": w.caseFor(t, w.clean[0].ID), "decision": "DISTINCT_APPROVED"}, 403, "REVIEW_CONFLICT")
	})
	t.Run("主项目证据不完整", func(t *testing.T) {
		for _, mode := range []string{"projection", "frozen"} {
			w := buildTx(t, "合成事务主项目证据"+mode, nil)
			if mode == "projection" {
				g1Exec(t, w.f.db, `UPDATE intake_projects SET evidence_state='INCOMPLETE' WHERE id=$1`, w.primary.ID)
			} else {
				g1Exec(t, w.f.db, `ALTER TABLE project_submissions DISABLE TRIGGER submission_immutable`)
				g1Exec(t, w.f.db, `UPDATE project_submissions SET evidence_state='INCOMPLETE' WHERE project_id=$1`, w.primary.ID)
				g1Exec(t, w.f.db, `ALTER TABLE project_submissions ENABLE TRIGGER submission_immutable`)
			}
			for _, action := range []string{"approve", "return", "reject"} {
				w.reject(t, "主项目证据"+mode, w.f.director, "director", action, nil, 409, "REVIEW_EVIDENCE_INCOMPLETE")
			}
			for _, d := range both {
				w.reject(t, "主项目证据"+mode, w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": w.caseFor(t, w.clean[0].ID), "decision": d}, 409, "REVIEW_EVIDENCE_INCOMPLETE")
			}
		}
	})
	t.Run("历史候选证据不完整与确认", func(t *testing.T) {
		for _, d := range both {
			w := buildTx(t, "合成事务历史"+d, func(f g1Fixture) Project {
				p := g1Submit(t, f.sales, g1Create(t, f.sales, "合成事务历史"+d))
				g1Exec(t, f.db, `ALTER TABLE intake_projects DROP CONSTRAINT IF EXISTS submission_projection`)
				g1Exec(t, f.db, `UPDATE intake_projects SET intake_status='APPROVED',evidence_state='INCOMPLETE',submission_revision=0,submitted_by_subject=NULL WHERE id=$1`, p.ID)
				return p
			})
			sel := w.caseFor(t, w.clean[0].ID)
			if d == "DISTINCT_APPROVED" {
				w.reject(t, "历史确认缺失", w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": sel, "decision": d}, 409, "LEGACY_EVIDENCE_ACK_REQUIRED")
			}
			s, b := w.post(t, "历史候选"+d, w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": sel, "decision": d, "acknowledgeLegacyEvidence": d == "DISTINCT_APPROVED"})
			out := projectFrom(t, s, b)
			var details string
			_ = w.f.db.QueryRow(ctx, `SELECT details::text FROM audit_events WHERE project_id=$1 AND action LIKE 'duplicate.%'`, w.primary.ID).Scan(&details)
			var audit struct {
				Grades map[string]string `json:"candidateEvidence"`
				Ack    bool              `json:"legacyEvidenceAcknowledged"`
				Case   string            `json:"caseId"`
			}
			_ = json.Unmarshal([]byte(details), &audit)
			wantGrades := map[string]string{w.clean[0].ID: "COMPLETE_VERIFIED", w.clean[1].ID: "COMPLETE_VERIFIED", w.extra.ID: "LEGACY_PARTIAL_EVIDENCE"}
			if fmt.Sprint(audit.Grades) != fmt.Sprint(wantGrades) || audit.Ack != (d == "DISTINCT_APPROVED") || audit.Case != sel {
				t.Fatal("audit", details)
			}
			var pending, decided int
			_ = w.f.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE decision='PENDING'),count(*) FILTER (WHERE decision=$2) FROM duplicate_cases WHERE project_id=$1`, w.primary.ID, d).Scan(&pending, &decided)
			if d == "DISTINCT_APPROVED" && (out.IntakeStatus != "APPROVED" || pending != 0 || decided != 3) {
				t.Fatal("DISTINCT_APPROVED must decide every pending case and approve", out.IntakeStatus, pending, decided)
			}
			if d == "CONFIRMED_DUPLICATE" && (out.IntakeStatus != "REJECTED" || pending != 2 || decided != 1) {
				t.Fatal("CONFIRMED_DUPLICATE must decide only the selected case and reject", out.IntakeStatus, pending, decided)
			}
		}
	})
	t.Run("关联版本变化", func(t *testing.T) {
		w := buildTx(t, "合成事务版本", nil)
		sel := w.caseFor(t, w.clean[0].ID)
		stale := w.allVersions(t)
		s, b := w.f.sales.call("POST", "/api/v1/projects/"+w.clean[1].ID+"/activities", Activity{Version: w.clean[1].Version, Type: "PHONE", OccurredAt: time.Now().UTC(), Summary: "合成候选更新"})
		projectFrom(t, s, b)
		w.reject(t, "关联版本变化", w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": sel, "decision": "DISTINCT_APPROVED", "relatedVersions": stale}, 409, "RELATED_VERSION_CONFLICT")
		w.reject(t, "关联版本缺失", w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": sel, "decision": "DISTINCT_APPROVED", "relatedVersions": map[string]int{}}, 409, "RELATED_VERSION_CONFLICT")
		w.reject(t, "主项目旧版本", w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": sel, "decision": "DISTINCT_APPROVED", "version": w.primary.Version - 1}, 409, "VERSION_CONFLICT")
		w.reject(t, "未选候选", w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": "", "decision": "DISTINCT_APPROVED"}, 400, "INVALID_INPUT")
		w.reject(t, "不属于本项目的候选", w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": w.primary.ID, "decision": "DISTINCT_APPROVED"}, 409, "INVALID_STATE")
	})
	t.Run("跨组候选不可见", func(t *testing.T) {
		w := buildTx(t, "合成事务跨组", func(f g1Fixture) Project {
			c := login(t, f.server, "synthetic-c")
			return g1Submit(t, c, g1Create(t, c, "合成事务跨组"))
		})
		sel := w.caseFor(t, w.clean[0].ID)
		for _, d := range both {
			w.reject(t, "跨组候选", w.f.manager, "manager", "duplicate-decisions", map[string]any{"caseId": sel, "decision": d}, 403, "REVIEW_RELATED_PROJECT_RESTRICTED")
		}
		w.reject(t, "跨组候选", w.f.manager, "manager", "duplicate-decisions", map[string]any{"caseId": w.caseFor(t, w.extra.ID), "decision": "CONFIRMED_DUPLICATE"}, 403, "REVIEW_RELATED_PROJECT_RESTRICTED")
		w.reject(t, "跨组候选", w.f.manager, "manager", "approve", nil, 409, "DUPLICATE_DECISION_REQUIRED")
		s, b := w.post(t, "跨组候选", w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": sel, "decision": "DISTINCT_APPROVED"})
		if out := projectFrom(t, s, b); out.IntakeStatus != "APPROVED" {
			t.Fatal(out.IntakeStatus)
		}
	})
	if path := os.Getenv("BAOBEI_G32C_TX_TRANSCRIPT"); path != "" {
		_ = os.WriteFile(path, []byte(g32cTxTranscript.String()), 0o600)
	}
}

// TestDatabaseG32CReviewPostQueryScaling fails on the pre-G3-2C code, where
// the decision transaction read, checked and graded each candidate separately.
func TestDatabaseG32CReviewPostQueryScaling(t *testing.T) {
	got := map[string]map[int]int64{"DISTINCT_APPROVED": {}, "CONFIRMED_DUPLICATE": {}, "approve": {}}
	for _, n := range []int{1, 10, 50, 200} {
		for d := range got {
			name := fmt.Sprintf("合成事务规模%s%03d", d, n)
			spec := g32cSpec{readable: n}
			if d == "approve" {
				spec = g32cSpec{} // no candidates: plain approval for reference
			}
			w := g32cBuild(t, name, spec)
			tr := newTracedApp(t, w.f.db)
			dir := login(t, tr.srv, "synthetic-director")
			p := w.primary
			in := map[string]any{"version": p.Version, "reason": "合成规模"}
			action := "approve"
			if d != "approve" {
				action = "duplicate-decisions"
				in["relatedVersions"] = relatedVersionsFor(dir, p)
				in["caseId"] = comparisonCases(t, dir, p)[0].ID
				in["decision"] = d
			}
			q := tr.count(t, func() {
				s, b := dir.call("POST", "/api/v1/projects/"+p.ID+"/"+action, in)
				expect(t, s, 200, b)
			})
			got[d][n] = q
		}
	}
	t.Logf("REVIEW_POST_QUERIES %v", got)
	for d, m := range got {
		if m[1] != m[10] || m[1] != m[50] || m[1] != m[200] {
			t.Fatalf("%s POST query count grows with candidates: %v", d, m)
		}
	}
}

// TestDatabaseG32CReviewConcurrency: races, lock waits, client timeouts and an
// injected failure after the first writes. The advisory lock 727102 plus the
// UUID-ordered row locks must serialize decisions without deadlock and without
// partial results.
func TestDatabaseG32CReviewConcurrency(t *testing.T) {
	ctx := context.Background()
	t.Run("两名审核人同时决定", func(t *testing.T) {
		for round := 0; round < 6; round++ {
			w := buildTx(t, fmt.Sprintf("合成并发审核%d", round), nil)
			sel := w.caseFor(t, w.clean[0].ID)
			p := w.fresh(t)
			versions := w.allVersions(t)
			decisions := [][2]string{{"DISTINCT_APPROVED", "CONFIRMED_DUPLICATE"}, {"DISTINCT_APPROVED", "DISTINCT_APPROVED"}, {"CONFIRMED_DUPLICATE", "CONFIRMED_DUPLICATE"}}[round%3]
			var wg sync.WaitGroup
			codes := make([]int, 2)
			start := make(chan struct{})
			for i, c := range []testClient{w.f.manager, w.f.director} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					codes[i], _ = c.call("POST", "/api/v1/projects/"+p.ID+"/duplicate-decisions", map[string]any{"version": p.Version, "reason": "合成并发", "caseId": sel, "decision": decisions[i], "relatedVersions": versions})
				}()
			}
			close(start)
			wg.Wait()
			ok := 0
			for _, c := range codes {
				if c == 200 {
					ok++
				} else if c != 409 {
					t.Fatal("unexpected status", codes)
				}
			}
			var approvals, events, decidedBy int
			_ = w.f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM approvals WHERE project_id=$1),(SELECT count(*) FROM audit_events WHERE project_id=$1 AND action LIKE 'duplicate.%'),
 (SELECT count(DISTINCT decided_by_subject) FROM duplicate_cases WHERE project_id=$1 AND decision<>'PENDING')`, p.ID).Scan(&approvals, &events, &decidedBy)
			if ok != 1 || approvals != 1 || events != 1 || decidedBy != 1 {
				t.Fatalf("round %d: ok=%d approvals=%d events=%d deciders=%d codes=%v", round, ok, approvals, events, decidedBy, codes)
			}
			fmt.Fprintf(&g32cTxTranscript, "并发 round %d %v → %v\n", round, decisions, codes)
		}
	})
	// hold locks a candidate row in a separate transaction, starts the POST
	// (which must wait on that row), applies change and commits.
	hold := func(t *testing.T, w txWorld, candidate string, change string, client testClient, body map[string]any) (int, []byte, time.Duration) {
		t.Helper()
		tx, err := w.f.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `SELECT 1 FROM intake_projects WHERE id=$1 FOR UPDATE`, candidate); err != nil {
			t.Fatal(err)
		}
		type res struct {
			s int
			b []byte
		}
		done := make(chan res, 1)
		started := time.Now()
		go func() {
			s, b := client.call("POST", "/api/v1/projects/"+w.primary.ID+"/duplicate-decisions", body)
			done <- res{s, b}
		}()
		// Wait until the POST is blocked on our row lock.
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiting bool
			_ = w.f.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND datname=current_database())`).Scan(&waiting)
			if waiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("POST did not wait for the candidate row lock")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if change != "" {
			if _, err = tx.Exec(ctx, change, candidate); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case r := <-done:
			return r.s, r.b, time.Since(started)
		case <-time.After(12 * time.Second):
			t.Fatal("review POST deadlocked")
		}
		return 0, nil, 0
	}
	t.Run("审核中候选版本变化", func(t *testing.T) {
		w := buildTx(t, "合成并发版本", nil)
		body := map[string]any{"version": w.primary.Version, "reason": "合成", "caseId": w.caseFor(t, w.clean[0].ID), "decision": "DISTINCT_APPROVED", "relatedVersions": w.allVersions(t)}
		before := dbState(t, w.g32cWorld)
		s, b, waited := hold(t, w, w.clean[1].ID, `UPDATE intake_projects SET version=version+1,updated_at=now() WHERE id=$1`, w.f.director, body)
		fmt.Fprintf(&g32cTxTranscript, "锁等待中候选版本变化 → HTTP %d %s（等待 %s）\n", s, errCode(b), waited.Round(time.Millisecond))
		if s != 409 || errCode(b) != "RELATED_VERSION_CONFLICT" {
			t.Fatal(s, string(b))
		}
		// Only the external version bump is visible; the POST wrote nothing.
		after := dbState(t, w.g32cWorld)
		if strings.Count(after, "PENDING") != strings.Count(before, "PENDING") || strings.Contains(after, "APPROVED:") {
			t.Fatal("partial write", after)
		}
	})
	t.Run("审核中关联关系变化", func(t *testing.T) {
		w := buildTx(t, "合成并发归属", nil)
		body := map[string]any{"version": w.primary.Version, "reason": "合成", "caseId": w.caseFor(t, w.clean[0].ID), "decision": "DISTINCT_APPROVED", "relatedVersions": w.allVersions(t)}
		// The candidate moves to team two while the manager's POST waits.
		s, b, _ := hold(t, w, w.clean[1].ID, `UPDATE intake_projects SET owner_subject='synthetic-c',team_id='22222222-2222-4222-8222-222222222222',version=version+1 WHERE id=$1`, w.f.manager, body)
		fmt.Fprintf(&g32cTxTranscript, "锁等待中候选转出本组 → HTTP %d %s\n", s, errCode(b))
		if s != 403 || errCode(b) != "REVIEW_RELATED_PROJECT_RESTRICTED" {
			t.Fatal(s, string(b))
		}
		var pending int
		_ = w.f.db.QueryRow(ctx, `SELECT count(*) FROM duplicate_cases WHERE project_id=$1 AND decision='PENDING'`, w.primary.ID).Scan(&pending)
		if pending != 2 {
			t.Fatal("partial decision", pending)
		}
	})
	t.Run("锁等待超时回滚", func(t *testing.T) {
		w := buildTx(t, "合成并发超时", nil)
		before := dbState(t, w.g32cWorld)
		tx, err := w.f.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT 1 FROM intake_projects WHERE id=$1 FOR UPDATE`, w.clean[0].ID); err != nil {
			t.Fatal(err)
		}
		// A client that gives up after 700 ms: the server request context is
		// cancelled while the transaction waits for the row lock.
		jar, _ := cookiejar.New(nil)
		impatient := w.f.director
		impatient.client = &http.Client{Jar: jar, Timeout: 700 * time.Millisecond}
		for _, ck := range w.f.director.client.Jar.Cookies(mustURL(w.f.server.URL)) {
			jar.SetCookies(mustURL(w.f.server.URL), []*http.Cookie{ck})
		}
		body := map[string]any{"version": w.primary.Version, "reason": "合成", "caseId": w.caseFor(t, w.clean[0].ID), "decision": "DISTINCT_APPROVED", "relatedVersions": w.allVersions(t)}
		req, _ := http.NewRequest("POST", w.f.server.URL+"/api/v1/projects/"+w.primary.ID+"/duplicate-decisions", strings.NewReader(string(mustJSON(body))))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", w.f.server.URL)
		req.Header.Set("X-CSRF-Token", w.f.director.csrf)
		if resp, err := impatient.client.Do(req); err == nil {
			resp.Body.Close()
			t.Fatal("request should time out while waiting", resp.StatusCode)
		}
		// The server side must abandon the wait and release everything.
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiters int
			_ = w.f.db.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND datname=current_database()`).Scan(&waiters)
			if waiters == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("cancelled review still waits for locks")
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = tx.Rollback(ctx)
		if after := dbState(t, w.g32cWorld); after != before {
			t.Fatal("timed-out review wrote data", before, after)
		}
		s, b := w.post(t, "超时后重试", w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": w.caseFor(t, w.clean[0].ID), "decision": "DISTINCT_APPROVED"})
		if out := projectFrom(t, s, b); out.IntakeStatus != "APPROVED" {
			t.Fatal(out.IntakeStatus)
		}
	})
	t.Run("写入中途异常整体回滚", func(t *testing.T) {
		for _, d := range []string{"DISTINCT_APPROVED", "CONFIRMED_DUPLICATE"} {
			w := buildTx(t, "合成并发异常"+d, nil)
			before := dbState(t, w.g32cWorld)
			// Test-only trigger in the isolated schema: the audit insert (the
			// last write of the transaction) fails after cases, project and
			// approvals were already updated.
			g1Exec(t, w.f.db, `CREATE FUNCTION g32c_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'g32c injected'; END $$`)
			g1Exec(t, w.f.db, `CREATE TRIGGER g32c_fail BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION g32c_fail()`)
			s, b := w.post(t, "注入异常"+d, w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": w.caseFor(t, w.clean[0].ID), "decision": d})
			if s != 503 {
				t.Fatal(s, string(b))
			}
			g1Exec(t, w.f.db, `DROP TRIGGER g32c_fail ON audit_events`)
			if after := dbState(t, w.g32cWorld); after != before {
				t.Fatalf("partial decision after injected failure\nbefore %s\nafter  %s", before, after)
			}
			s, b = w.post(t, "异常后重试"+d, w.f.director, "director", "duplicate-decisions", map[string]any{"caseId": w.caseFor(t, w.clean[0].ID), "decision": d})
			projectFrom(t, s, b)
		}
	})
	t.Run("多项目共享候选交叉并发无死锁", func(t *testing.T) {
		f := g1FixtureFor(t)
		name := "合成并发交叉"
		var cands []Project
		for i := 0; i < 12; i++ {
			cands = append(cands, g1Submit(t, f.sales, g1Create(t, f.sales, name)))
		}
		var primaries []Project
		for i := 0; i < 3; i++ {
			primaries = append(primaries, g1Submit(t, f.other, g1Create(t, f.other, name)))
		}
		var wg sync.WaitGroup
		errs := make(chan string, 64)
		run := func(fn func() (int, []byte), allowed ...int) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, b := fn()
				for _, a := range allowed {
					if s == a {
						return
					}
				}
				errs <- fmt.Sprintf("HTTP %d %s", s, b)
			}()
		}
		for _, p := range primaries {
			p := p
			for _, c := range []testClient{f.director, f.manager} {
				c := c
				run(func() (int, []byte) {
					cs := comparisonCases(t, c, p)
					return c.call("POST", "/api/v1/projects/"+p.ID+"/duplicate-decisions", map[string]any{"version": p.Version, "reason": "合成交叉", "caseId": cs[len(cs)-1].ID, "decision": "DISTINCT_APPROVED", "relatedVersions": relatedVersionsFor(c, p), "acknowledgeLegacyEvidence": true})
				}, 200, 409)
			}
		}
		// Concurrent row-level activity on shared candidates (no 727102 lock).
		for i := len(cands) - 1; i >= 0; i -= 3 {
			c := cands[i]
			run(func() (int, []byte) {
				return f.sales.call("POST", "/api/v1/projects/"+c.ID+"/activities", Activity{Version: c.Version, Type: "PHONE", OccurredAt: time.Now().UTC(), Summary: "合成交叉跟进"})
			}, 200, 409)
		}
		// A concurrent submission takes 727102 like the decisions.
		run(func() (int, []byte) {
			p := g1Create(t, f.sales, name)
			return f.sales.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
		}, 200)
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("deadlock or starvation")
		}
		close(errs)
		for e := range errs {
			t.Fatal(e)
		}
		defer func() {
			if path := os.Getenv("BAOBEI_G32C_TX_TRANSCRIPT"); path != "" {
				_ = os.WriteFile(path, []byte(g32cTxTranscript.String()), 0o600)
			}
		}()
		for _, p := range primaries {
			var events, approvals int
			var status string
			_ = f.db.QueryRow(ctx, `SELECT intake_status,(SELECT count(*) FROM audit_events WHERE project_id=$1 AND action LIKE 'duplicate.%'),(SELECT count(*) FROM approvals WHERE project_id=$1) FROM intake_projects WHERE id=$1`, p.ID).Scan(&status, &events, &approvals)
			if (status == "APPROVED") != (events == 1 && approvals == 1) || events > 1 {
				t.Fatal("illegal audit trail", p.ID, status, events, approvals)
			}
		}
	})
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
