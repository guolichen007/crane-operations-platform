package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// V1.1 R1: a supervisor files, edits, submits and follows up their own
// projects; a director decides them; nobody reviews their own filing; a
// supervisor never writes a team member's project; staff changes keep owners.

func v11Get(t *testing.T, c testClient, id string) Project {
	t.Helper()
	s, b := c.call("GET", "/api/v1/projects/"+id, nil)
	return projectFrom(t, s, b)
}

func v11Code(t *testing.T, status int, b []byte, want int, code string) {
	t.Helper()
	if status != want || code != "" && !strings.Contains(string(b), `"`+code+`"`) {
		t.Fatalf("HTTP %d want %d %s: %s", status, want, code, b)
	}
}

func staffIDOf(t *testing.T, db *pgxpool.Pool, subject string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(), `SELECT staff_id::text FROM app_users WHERE subject=$1`, subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDatabaseV11SupervisorFiling(t *testing.T) {
	ctx := context.Background()
	f := g1FixtureFor(t)
	ma, mb := f.manager, login(t, f.server, "synthetic-manager-other")
	a1, a2, d1 := f.sales, f.other, f.director
	c1 := login(t, f.server, "synthetic-c")
	const teamA = "11111111-1111-4111-8111-111111111111"

	// AC-06: create (owner, team, creator from the session) with idempotent replay.
	in := sample("V11主管本人项目")
	in.Customer = "V11主管客户甲"
	key := randomToken()[:32]
	s, b := ma.keyed(key, in)
	p := projectFrom(t, s, b)
	if p.OwnerSubject != "synthetic-manager" || p.CreatedBySubject != "synthetic-manager" || p.TeamID != teamA || p.OwnerRole != managerRole || p.ReviewRoute != routeDirector {
		t.Fatalf("supervisor project: %+v", p)
	}
	s, b = ma.keyed(key, in)
	if replay := projectFrom(t, s, b); replay.ID != p.ID || !replay.Replayed {
		t.Fatal("idempotent replay created another project")
	}
	if n := countRows(t, f.db, `SELECT count(*) FROM intake_projects WHERE created_by_subject='synthetic-manager'`); n != 1 {
		t.Fatal("projects created", n)
	}
	// A supervisor cannot assign or leave a draft unassigned; a director cannot file.
	for _, extra := range []map[string]any{{"assignmentMode": "ASSIGN_ACTIVE_SALES", "targetSubject": "synthetic-a"}, {"assignmentMode": "UNASSIGNED_DRAFT"}} {
		body := map[string]any{}
		raw := mustJSON(sample("V11越权指派"))
		_ = jsonUnmarshal(raw, &body)
		for k, v := range extra {
			body[k] = v
		}
		s, b = ma.call("POST", "/api/v1/projects", body)
		v11Code(t, s, b, 403, "BUSINESS_PERMISSION_DENIED")
	}
	s, b = d1.call("POST", "/api/v1/projects", sample("V11总监本人"))
	v11Code(t, s, b, 403, "BUSINESS_PERMISSION_DENIED")
	// An administrator still assigns only to sales members.
	s, b = f.admin.call("POST", "/api/v1/projects", map[string]any{"projectName": "V11指派主管", "customer": "V11指派主管客户", "salesStage": "L0_LEAD", "assignmentMode": "ASSIGN_ACTIVE_SALES", "targetSubject": "synthetic-manager"})
	v11Code(t, s, b, 409, "OWNER_INACTIVE")
	// The wizard duplicate check is available to supervisors.
	s, b = ma.call("POST", "/api/v1/duplicate-checks", in)
	expect(t, s, 200, b)

	// Edit, then submit: frozen submission plus an immutable DIRECTOR route.
	edit := p.Input
	edit.Version = p.Version
	edit.NextAction = "主管本人跟进"
	s, b = ma.call("PATCH", "/api/v1/projects/"+p.ID, edit)
	p = projectFrom(t, s, b)
	if !p.Actions["submit"].Allowed || p.Actions["approve"].Allowed {
		t.Fatalf("owner hints %+v", p.Actions)
	}
	p = g1Submit(t, ma, p)
	if p.IntakeStatus != "SUBMITTED" || p.SubmissionRevision != 1 {
		t.Fatal("submit", p.IntakeStatus)
	}
	var route, ownerRole, basis string
	if err := f.db.QueryRow(ctx, `SELECT route,owner_role,basis FROM project_review_routes WHERE project_id=$1 AND submission_revision=1`, p.ID).Scan(&route, &ownerRole, &basis); err != nil || route != routeDirector || ownerRole != managerRole || basis != "SUBMISSION" {
		t.Fatal("route evidence", route, ownerRole, basis, err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE project_review_routes SET route='TEAM_MANAGER' WHERE project_id=$1`, p.ID); !isCode(err, "BB002") {
		t.Fatal("route evidence is mutable", err)
	}

	// AC-07: self review is refused, nothing is recorded.
	before := countRows(t, f.db, `SELECT count(*) FROM approvals`)
	for _, action := range []string{"approve", "return", "reject"} {
		s, b = g1Decision(ma, p, action, nil)
		v11Code(t, s, b, 403, "REVIEW_CONFLICT")
	}
	if v11Get(t, ma, p.ID).Review.ReasonCode == nil {
		t.Fatal("own filing shown as reviewable")
	}
	// A second supervisor of the same team is not a director.
	g1Exec(t, f.db, `UPDATE app_users SET team_id=$1 WHERE subject='synthetic-manager-other'`, teamA)
	same := v11Get(t, mb, p.ID)
	if same.Actions["approve"].Allowed || *same.Actions["approve"].ReasonCode != "REVIEW_ROUTE_DIRECTOR" || same.Review == nil || same.Review.Eligible {
		t.Fatalf("same-team supervisor hint %+v", same.Actions["approve"])
	}
	s, b = g1Decision(mb, same, "approve", nil)
	v11Code(t, s, b, 403, "REVIEW_ROUTE_DIRECTOR")
	q := reviewQueueOf(t, mb, "reviewable")
	for _, item := range q.Items {
		if item.ID == p.ID {
			t.Fatal("director-routed filing in a supervisor's reviewable queue")
		}
	}
	g1Exec(t, f.db, `UPDATE app_users SET team_id='22222222-2222-4222-8222-222222222222' WHERE subject='synthetic-manager-other'`)
	// AC-10: other team and sales cannot decide.
	s, b = g1Decision(mb, p, "approve", nil)
	v11Code(t, s, b, 404, "")
	s, b = g1Decision(a1, p, "approve", nil)
	v11Code(t, s, b, 403, "")
	if after := countRows(t, f.db, `SELECT count(*) FROM approvals`); after != before {
		t.Fatal("refused decisions recorded approvals", before, after)
	}

	// AC-08: the director's queue and decision.
	found := false
	for _, item := range reviewQueueOf(t, d1, "reviewable").Items {
		found = found || item.ID == p.ID
	}
	if !found {
		t.Fatal("director queue misses the supervisor filing")
	}
	dp := v11Get(t, d1, p.ID)
	if !dp.Actions["approve"].Allowed || !dp.Review.Eligible {
		t.Fatal("director hint", dp.Actions["approve"])
	}
	s, b = g1Decision(d1, dp, "approve", nil)
	p = projectFrom(t, s, b)
	if p.IntakeStatus != "APPROVED" {
		t.Fatal(p.IntakeStatus)
	}
	// Replay with the old version is a version conflict, never a second approval.
	s, b = g1Decision(d1, dp, "approve", nil)
	v11Code(t, s, b, 409, "VERSION_CONFLICT")
	if n := countRows(t, f.db, `SELECT count(*) FROM approvals WHERE project_id='`+p.ID+`'`); n != 1 {
		t.Fatal("approvals", n)
	}

	// The supervisor follows up and advances their own project.
	p = v11Get(t, ma, p.ID)
	if !p.Actions["activities"].Allowed || !p.Actions["stage-change"].Allowed {
		t.Fatalf("own follow-up %+v", p.Actions)
	}
	s, b = ma.call("POST", "/api/v1/projects/"+p.ID+"/activities", map[string]any{"version": p.Version, "type": "PHONE", "occurredAt": "2026-11-16T02:00:00Z", "summary": "主管电话沟通", "result": "确认需求", "nextAction": "安排技术交流", "nextFollowupAt": "2026-11-20T02:00:00Z"})
	p = projectFrom(t, s, b)
	s, b = g1Decision(ma, p, "stage-change", map[string]any{"salesStage": "L2_TECHNICAL"})
	p = projectFrom(t, s, b)
	if p.SalesStage != "L2_TECHNICAL" {
		t.Fatal(p.SalesStage)
	}

	// AC-09: the supervisor reviews a team sales filing but never writes it.
	sp := g1Create(t, a1, "V11销售项目")
	sp = g1Submit(t, a1, sp)
	mv := v11Get(t, ma, sp.ID)
	if mv.ReviewRoute != routeTeamManager || !mv.Actions["approve"].Allowed {
		t.Fatalf("team filing for supervisor %+v", mv.Actions)
	}
	for _, action := range []string{"edit", "submit", "activities", "stage-change", "discard"} {
		if mv.Actions[action].Allowed {
			t.Fatal("supervisor may write a member project:", action)
		}
	}
	s, b = ma.call("POST", "/api/v1/projects/"+sp.ID+"/activities", map[string]any{"version": mv.Version, "type": "PHONE", "occurredAt": "2026-11-16T02:00:00Z", "summary": "代写", "result": "", "nextAction": ""})
	v11Code(t, s, b, 403, "BUSINESS_PERMISSION_DENIED")
	s, b = g1Decision(ma, mv, "stage-change", map[string]any{"salesStage": "L2_TECHNICAL"})
	v11Code(t, s, b, 403, "")
	edit = mv.Input
	edit.Version = mv.Version
	s, b = ma.call("PATCH", "/api/v1/projects/"+sp.ID, edit)
	v11Code(t, s, b, 403, "")
	s, b = g1Decision(ma, mv, "approve", nil)
	if projectFrom(t, s, b).IntakeStatus != "APPROVED" {
		t.Fatal("supervisor approval of a sales filing")
	}

	// AC-02/AC-05 privacy of the supervisor project: other team and other sales.
	for _, c := range []testClient{mb, c1, a2} {
		s, b = c.call("GET", "/api/v1/projects/"+p.ID, nil)
		v11Code(t, s, b, 404, "")
		if l := listOf(t, c, "q=V11主管本人项目&pageSize=100"); l.Total != 0 {
			t.Fatal("listed elsewhere")
		}
	}

	// Supervisor transfers: own project and a member's, never another supervisor's.
	g1Exec(t, f.db, `UPDATE app_users SET team_id=$1 WHERE subject='synthetic-manager-other'`, teamA)
	other := g1Create(t, mb, "V11同组主管项目")
	s, b = g1Decision(ma, v11Get(t, ma, other.ID), "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
	v11Code(t, s, b, 403, "")
	s, b = g1Decision(ma, v11Get(t, ma, other.ID), "ownership-transfer", map[string]any{"targetSubject": "synthetic-manager"})
	v11Code(t, s, b, 403, "")
	// Transfer target must be a sales member, even for directors.
	s, b = g1Decision(d1, v11Get(t, d1, other.ID), "ownership-transfer", map[string]any{"targetSubject": "synthetic-manager"})
	v11Code(t, s, b, 409, "OWNER_INACTIVE")

	// A route never drops: a supervisor filing handed to a sales member during
	// review stays with directors.
	second := g1Submit(t, mb, other)
	s, b = g1Decision(d1, v11Get(t, d1, second.ID), "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
	moved := projectFrom(t, s, b)
	if moved.OwnerSubject != "synthetic-b" || moved.CreatedBySubject != "synthetic-manager-other" || moved.ReviewRoute != routeDirector {
		t.Fatalf("handed-off filing %+v", moved.ReviewRoute)
	}
	s, b = g1Decision(ma, v11Get(t, ma, second.ID), "approve", nil)
	v11Code(t, s, b, 403, "REVIEW_ROUTE_DIRECTOR")
	// A sales filer promoted while waiting is reviewed by directors too.
	waiting := g1Submit(t, a2, g1Create(t, a2, "V11晋升前提交"))
	g1Exec(t, f.db, `UPDATE app_users SET role='baobei_manager' WHERE subject='synthetic-b'`)
	s, b = g1Decision(ma, v11Get(t, ma, waiting.ID), "approve", nil)
	v11Code(t, s, b, 403, "REVIEW_ROUTE_DIRECTOR")
	g1Exec(t, f.db, `UPDATE app_users SET role='baobei_sales' WHERE subject='synthetic-b'`)
	g1Exec(t, f.db, `UPDATE app_users SET team_id='22222222-2222-4222-8222-222222222222' WHERE subject='synthetic-manager-other'`)

	// Return and resubmit: a new frozen revision with its own DIRECTOR route.
	r := g1Submit(t, ma, g1Create(t, ma, "V11退回重提"))
	s, b = g1Decision(d1, v11Get(t, d1, r.ID), "return", nil)
	r = projectFrom(t, s, b)
	r = g1Submit(t, ma, v11Get(t, ma, r.ID))
	if n := countRows(t, f.db, `SELECT count(*) FROM project_review_routes WHERE project_id='`+r.ID+`' AND route='DIRECTOR'`); n != 2 || r.SubmissionRevision != 2 {
		t.Fatal("resubmission routes", n, r.SubmissionRevision)
	}

	// AC-11: staff guard. The supervisor still owns open projects: a team change
	// needs a handoff first; suspension queues the handoff; an administrator
	// hands the projects to a sales member; evidence stays identical.
	maID := staffIDOf(t, f.db, "synthetic-manager")
	var version int
	_ = f.db.QueryRow(ctx, `SELECT staff_version FROM app_users WHERE subject='synthetic-manager'`).Scan(&version)
	s, b = f.admin.call("PATCH", "/api/v1/staff/"+maID, map[string]any{"name": "示例主管", "role": managerRole, "teamId": "22222222-2222-4222-8222-222222222222", "reason": "调组", "version": version})
	v11Code(t, s, b, 409, "STAFF_HANDOFF_REQUIRED")
	evidence := func() string {
		var h string
		if err := f.db.QueryRow(ctx, `SELECT md5(string_agg(project_id::text||revision||actor_subject||creator_subject||owner_subject||document_hash,',' ORDER BY project_id,revision)) FROM project_submissions WHERE creator_subject='synthetic-manager'`).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	frozen := evidence()
	s, b = f.admin.call("POST", "/api/v1/staff/"+maID+"/suspend", map[string]any{"version": version, "reason": "停用前移交"})
	expect(t, s, 200, b)
	if n := countRows(t, f.db, `SELECT count(*) FROM staff_handoffs WHERE staff_id='`+maID+`'`); n != 2 {
		t.Fatal("queued handoffs", n)
	}
	var detail struct {
		Member   staffMember    `json:"member"`
		Projects []staffProject `json:"projects"`
	}
	s, b = f.admin.call("GET", "/api/v1/staff/"+maID, nil)
	expect(t, s, 200, b)
	_ = jsonUnmarshal(b, &detail)
	items := []map[string]any{}
	for _, pr := range detail.Projects {
		items = append(items, map[string]any{"id": pr.ID, "version": pr.Version})
	}
	if len(items) != 2 {
		t.Fatal("open projects of the supervisor", len(items))
	}
	s, b = f.admin.call("POST", "/api/v1/staff/"+maID+"/ownership-transfer-batch", map[string]any{"targetId": staffIDOf(t, f.db, "synthetic-a"), "version": detail.Member.Version, "reason": "主管停用移交", "projects": items})
	expect(t, s, 200, b)
	if strings.Count(string(b), `"TRANSFERRED"`) != 2 {
		t.Fatal(string(b))
	}
	if n := countRows(t, f.db, `SELECT count(*) FROM intake_projects WHERE owner_subject='synthetic-manager' AND `+openProjects); n != 0 {
		t.Fatal("orphaned supervisor projects", n)
	}
	if n := countRows(t, f.db, `SELECT count(*) FROM intake_projects WHERE created_by_subject='synthetic-manager' AND owner_subject='synthetic-a'`); n != 2 {
		t.Fatal("creator changed or not transferred", n)
	}
	if evidence() != frozen {
		t.Fatal("frozen submissions changed by the handoff")
	}
}

func TestDatabaseV11RouteBackfillFrom00008(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	old := "v11_old_" + hash(randomToken())[:12]
	if _, err := db.Exec(ctx, `CREATE SCHEMA `+old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DROP SCHEMA `+old+` CASCADE`) })
	cfg := db.Config().Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = old + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `CREATE TABLE baobei_migrations(name text PRIMARY KEY, checksum text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for _, e := range entries {
		if e.Name() >= "00009" {
			break
		}
		body, _ := migrations.ReadFile("migrations/" + e.Name())
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(e.Name(), err)
		}
		h := sha256.Sum256(body)
		if _, err = pool.Exec(ctx, `INSERT INTO baobei_migrations VALUES($1,$2)`, e.Name(), hex.EncodeToString(h[:])); err != nil {
			t.Fatal(err)
		}
	}
	// 00008 data: one sales filing with two frozen revisions and one draft.
	for _, sql := range []string{
		`INSERT INTO teams(id,name) VALUES('44444444-4444-4444-8444-444444444444','旧团队')`,
		`INSERT INTO app_users(subject,display_name,role,team_id) VALUES('old-sales','旧销售','baobei_sales','44444444-4444-4444-8444-444444444444')`,
		`INSERT INTO intake_projects(id,project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,document,sales_stage,estimated_amount)
		 VALUES('55555555-5555-4555-8555-555555555555','旧报备','旧报备','old-sales','old-sales','old-sales','44444444-4444-4444-8444-444444444444','{}','L5_QUOTATION',1234.56),
		 ('66666666-6666-4666-8666-666666666666','旧草稿','旧草稿','old-sales','old-sales','old-sales','44444444-4444-4444-8444-444444444444','{}','HOLD',NULL)`,
		`INSERT INTO project_submissions(project_id,revision,source_version,actor_subject,creator_subject,owner_subject,team_id,document,document_hash,participants,evidence_state)
		 SELECT '55555555-5555-4555-8555-555555555555',r,1,'old-sales','old-sales','old-sales','44444444-4444-4444-8444-444444444444','{}',repeat('a',64),'[]','COMPLETE' FROM generate_series(1,2) r`,
		`UPDATE intake_projects SET submitted_by_subject='old-sales',submission_revision=2,intake_status='SUBMITTED' WHERE id='55555555-5555-4555-8555-555555555555'`,
	} {
		if _, err = pool.Exec(ctx, sql); err != nil {
			t.Fatal(sql, err)
		}
	}
	fingerprint := func() string {
		var h string
		if err := pool.QueryRow(ctx, `SELECT md5((SELECT string_agg(id::text||owner_subject||created_by_subject||coalesce(submitted_by_subject,'')||team_id::text||sales_stage||coalesce(estimated_amount::text,'∅')||intake_status||version,',' ORDER BY id) FROM intake_projects)
		 ||(SELECT string_agg(project_id::text||revision||document_hash||created_at::text,',' ORDER BY project_id,revision) FROM project_submissions))`).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	beforeUpgrade := fingerprint()
	if err = migrate(ctx, pool); err != nil {
		t.Fatal("upgrade 00008→00009", err)
	}
	if err = verifySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// AC-24: projects, stages, amounts and frozen evidence are unchanged.
	if fingerprint() != beforeUpgrade {
		t.Fatal("upgrade changed project data or evidence")
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM project_review_routes r JOIN project_submissions s ON s.project_id=r.project_id AND s.revision=r.submission_revision
	 WHERE r.route='TEAM_MANAGER' AND r.basis='BACKFILL_00009' AND r.owner_subject=s.owner_subject AND r.team_id=s.team_id AND r.created_at=s.created_at`).Scan(&n); err != nil || n != 2 {
		t.Fatal("backfill", n, err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM project_review_routes`)
	if !isCode(err, "BB002") {
		t.Fatal("backfilled route deletable", err)
	}
	// An owner role mismatch can never be stored.
	_, err = pool.Exec(ctx, `INSERT INTO app_users(subject,display_name,role,team_id) VALUES('old-manager','旧主管','baobei_manager','44444444-4444-4444-8444-444444444444')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO project_submissions(project_id,revision,source_version,actor_subject,creator_subject,owner_subject,team_id,document,document_hash,participants,evidence_state)
	 VALUES('66666666-6666-4666-8666-666666666666',1,1,'old-manager','old-manager','old-manager','44444444-4444-4444-8444-444444444444','{}',repeat('b',64),'[]','COMPLETE')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO project_review_routes(project_id,submission_revision,owner_subject,owner_role,team_id,route,basis) VALUES('66666666-6666-4666-8666-666666666666',1,'old-manager','baobei_manager','44444444-4444-4444-8444-444444444444','TEAM_MANAGER','SUBMISSION')`)
	if !isCode(err, "23514") {
		t.Fatal("supervisor filing stored with a supervisor route", err)
	}
	if err = migrate(ctx, pool); err != nil {
		t.Fatal("re-run", err)
	}
}

func isCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func reviewQueueOf(t *testing.T, c testClient, eligibility string) ReviewQueue {
	t.Helper()
	s, b := c.call("GET", "/api/v1/reviews?eligibility="+eligibility+"&pageSize=100", nil)
	expect(t, s, 200, b)
	var q ReviewQueue
	if err := json.Unmarshal(b, &q); err != nil {
		t.Fatal(err)
	}
	return q
}
