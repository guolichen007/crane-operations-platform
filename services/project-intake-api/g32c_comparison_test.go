package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// G3-2C. legacyComparison and legacyEvidenceGrade are verbatim copies of the
// pre-G3-2C implementation (8601f1b governance.go). They are the semantic
// oracle: the batched comparison must produce byte-identical JSON, the same
// Restricted flags and the same HTTP body for every role and data shape.
func legacyEvidenceGrade(ctx context.Context, q querier, p Project) (string, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT `+primaryCompleteSQL("p")+` FROM intake_projects p WHERE p.id=$1`, p.ID).Scan(&ok)
	if err != nil {
		return "", err
	}
	if ok {
		return "COMPLETE_VERIFIED", nil
	}
	return "LEGACY_PARTIAL_EVIDENCE", nil
}
func legacyComparison(ctx context.Context, q querier, p Project, u User) ([]Comparison, error) {
	rows, e := q.Query(ctx, `SELECT d.id::text,d.candidate_id::text,d.score,d.risk,d.matched_signals,d.decision FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id
 WHERE d.project_id=$1 AND (d.submission_revision=$2 OR d.decision<>'PENDING') ORDER BY d.created_at DESC,c.registration_no,d.id`, p.ID, p.SubmissionRevision)
	if e != nil {
		return nil, e
	}
	out := []Comparison{}
	ids := []string{}
	for rows.Next() {
		var c Comparison
		var id string
		var b []byte
		if e = rows.Scan(&c.ID, &id, &c.Score, &c.Risk, &b, &c.Decision); e != nil {
			rows.Close()
			return nil, e
		}
		if e = json.Unmarshal(b, &c.Signals); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, c)
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i, id := range ids {
		candidate, e := get(ctx, q, id, false)
		if e != nil {
			return nil, e
		}
		if canRead(u, candidate) {
			out[i].Candidate = &candidate
			if out[i].EvidenceGrade, e = legacyEvidenceGrade(ctx, q, candidate); e != nil {
				return nil, e
			}
		} else {
			out[i].Restricted = true
		}
	}
	return out, nil
}

// g32cSpec describes one comparison fixture built through the real API.
type g32cSpec struct {
	readable, hidden int  // candidates owned by team one (synthetic-a) / team two (synthetic-c)
	legacy           int  // first N readable and first N hidden become migrated INCOMPLETE rows
	decided          bool // director confirms the first visible case afterwards (primary REJECTED)
	tie              bool // synthetic: extra case rows with identical created_at and candidate
}

type g32cWorld struct {
	f       g1Fixture
	primary Project
	users   map[string]User
	clients map[string]testClient
}

func g32cUsers(t *testing.T, db *pgxpool.Pool) map[string]User {
	t.Helper()
	out := map[string]User{}
	for who, subject := range map[string]string{"manager": "synthetic-manager", "other-manager": "synthetic-manager-other", "director": "synthetic-director", "admin": "synthetic-admin", "sales": "synthetic-b"} {
		var u User
		if err := db.QueryRow(context.Background(), `SELECT subject,display_name,role,coalesce(team_id::text,'') FROM app_users WHERE subject=$1`, subject).Scan(&u.Subject, &u.Name, &u.Role, &u.TeamID); err != nil {
			t.Fatal(err)
		}
		out[who] = u
	}
	return out
}

// g32cBuild creates the candidates and the primary through the HTTP API.
// Only the legacy downgrade and the tie rows are direct SQL (labelled synthetic).
func g32cBuild(t *testing.T, name string, s g32cSpec) g32cWorld {
	t.Helper()
	f := g1FixtureFor(t)
	c := login(t, f.server, "synthetic-c")
	var readable, hidden []Project
	for i := 0; i < s.readable || i < s.hidden; i++ {
		if i < s.readable {
			readable = append(readable, g1Submit(t, f.sales, g1Create(t, f.sales, name)))
		}
		if i < s.hidden {
			hidden = append(hidden, g1Submit(t, c, g1Create(t, c, name)))
		}
	}
	if s.legacy > 0 {
		g1Exec(t, f.db, `ALTER TABLE intake_projects DROP CONSTRAINT IF EXISTS submission_projection`)
		for _, set := range [][]Project{readable, hidden} {
			for i := 0; i < s.legacy && i < len(set); i++ {
				g1Exec(t, f.db, `UPDATE intake_projects SET intake_status='APPROVED',evidence_state='INCOMPLETE',submission_revision=0,submitted_by_subject=NULL WHERE id=$1`, set[i].ID)
			}
		}
	}
	primary := g1Submit(t, f.other, g1Create(t, f.other, name))
	var cases int
	_ = f.db.QueryRow(context.Background(), `SELECT count(*) FROM duplicate_cases WHERE project_id=$1`, primary.ID).Scan(&cases)
	if cases != s.readable+s.hidden {
		t.Fatalf("fixture %s: %d cases want %d", name, cases, s.readable+s.hidden)
	}
	if s.tie && len(readable) > 0 {
		// Same candidate and same created_at: only d.id can order these rows.
		for i := 0; i < 3; i++ {
			g1Exec(t, f.db, `INSERT INTO duplicate_cases(project_id,candidate_id,score,risk,matched_signals,submission_revision,created_at)
 SELECT project_id,candidate_id,score,risk,matched_signals,submission_revision,created_at FROM duplicate_cases WHERE project_id=$1 AND candidate_id=$2`, primary.ID, readable[0].ID)
		}
	}
	w := g32cWorld{f: f, primary: primary, users: g32cUsers(t, f.db), clients: map[string]testClient{
		"manager": f.manager, "director": f.director, "admin": f.admin, "other-manager": login(t, f.server, "synthetic-manager-other"), "sales": f.other}}
	if s.decided {
		cases := comparisonCases(t, f.director, primary)
		s, b := g1Decision(f.director, primary, "duplicate-decisions", map[string]any{"caseId": cases[0].ID, "decision": "CONFIRMED_DUPLICATE"})
		w.primary = projectFrom(t, s, b)
	}
	return w
}

func comparisonJSON(t *testing.T, cs []Comparison) string {
	t.Helper()
	type row struct {
		Case       Comparison
		Restricted bool
	}
	rows := make([]row, len(cs))
	for i, c := range cs {
		rows[i] = row{c, c.Restricted}
	}
	return string(mustJSON(rows))
}

// legacyHTTPBody rebuilds the GET duplicate-comparison body with the oracle,
// using the same projection and encoder as the handler.
func legacyHTTPBody(t *testing.T, w g32cWorld, u User) []byte {
	t.Helper()
	ctx := context.Background()
	p, err := get(ctx, w.f.db, w.primary.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := legacyComparison(ctx, w.f.db, p, u)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.f.app.projectHints(ctx, w.f.db, u, &p); err != nil {
		t.Fatal(err)
	}
	visible, restricted := projectComparison(cases, u)
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{"project": p, "cases": visible, "restrictedRelated": restricted})
	return buf.Bytes()
}

var g32cTranscript strings.Builder

// assertComparisonEquivalent checks every role in-process and over HTTP.
func assertComparisonEquivalent(t *testing.T, label string, w g32cWorld) {
	t.Helper()
	ctx := context.Background()
	p, err := get(ctx, w.f.db, w.primary.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"manager", "other-manager", "director", "admin", "sales"} {
		u := w.users[who]
		want, err := legacyComparison(ctx, w.f.db, p, u)
		if err != nil {
			t.Fatal(err)
		}
		got, err := w.f.app.comparison(ctx, w.f.db, p, u)
		if err != nil {
			t.Fatal(err)
		}
		if a, b := comparisonJSON(t, want), comparisonJSON(t, got); a != b {
			t.Fatalf("%s %s comparison() differs from oracle:\nwant %s\ngot  %s", label, who, a, b)
		}
		status, body := w.clients[who].call("GET", "/api/v1/projects/"+p.ID+"/duplicate-comparison", nil)
		switch who {
		case "sales":
			expect(t, status, 403, body)
		case "other-manager":
			expect(t, status, 404, body) // primary is in team one
		default:
			expect(t, status, 200, body)
			if wantBody := legacyHTTPBody(t, w, u); !bytes.Equal(wantBody, body) {
				t.Fatalf("%s %s HTTP body differs from oracle:\nwant %s\ngot  %s", label, who, wantBody, body)
			}
		}
		visible, restricted := projectComparison(got, u)
		fmt.Fprintf(&g32cTranscript, "%s | %s | cases=%d visible=%d restricted=%v | %s\n", label, who, len(got), len(visible), restricted != nil, canonical(t, status, body))
	}
}

func TestDatabaseG32CComparisonEquivalence(t *testing.T) {
	scenarios := []struct {
		label string
		spec  g32cSpec
	}{
		{"无候选", g32cSpec{}},
		{"全部可读", g32cSpec{readable: 5}},
		{"全部不可读", g32cSpec{hidden: 5}},
		{"可读与不可读混合", g32cSpec{readable: 3, hidden: 3}},
		{"历史证据完整与不完整混合", g32cSpec{readable: 4, hidden: 2, legacy: 2}},
		{"已裁决与待裁决", g32cSpec{readable: 3, hidden: 2, legacy: 1, decided: true}},
		{"同时间同候选并列（合成）", g32cSpec{readable: 2, hidden: 1, tie: true}},
		{"候选1", g32cSpec{readable: 1}},
		{"候选10", g32cSpec{readable: 5, hidden: 5, legacy: 1}},
		{"候选50", g32cSpec{readable: 25, hidden: 25, legacy: 3}},
		{"候选200", g32cSpec{readable: 100, hidden: 100, legacy: 5}},
	}
	for i, sc := range scenarios {
		t.Run(sc.label, func(t *testing.T) {
			w := g32cBuild(t, fmt.Sprintf("合成对比等价%02d", i), sc.spec)
			assertComparisonEquivalent(t, sc.label, w)
		})
	}
	if path := os.Getenv("BAOBEI_G32C_TRANSCRIPT"); path != "" {
		_ = os.WriteFile(path, []byte(g32cTranscript.String()), 0o600)
	}
}

// tracedApp serves the API on a pool whose every statement is counted.
type tracedApp struct {
	pool    *pgxpool.Pool
	app     *App
	srv     *httptest.Server
	counter *atomic.Int64
}

func newTracedApp(t *testing.T, db *pgxpool.Pool) tracedApp {
	t.Helper()
	var n atomic.Int64
	cfg := db.Config().Copy()
	cfg.ConnConfig.Tracer = queryCounter{&n}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{db: pool, cfg: Config{Env: "development", Dev: true}}
	srv := httptest.NewServer(app.handler())
	app.cfg.Origin = srv.URL
	t.Cleanup(func() { srv.Close(); pool.Close() })
	return tracedApp{pool, app, srv, &n}
}

// count runs fn once and returns the number of statements it issued.
func (a tracedApp) count(t *testing.T, fn func()) int64 {
	t.Helper()
	a.counter.Store(0)
	fn()
	return a.counter.Load()
}

// TestDatabaseG32CComparisonQueryScaling fails on the pre-G3-2C code: there
// comparison() issued 1+2N statements and the HTTP request 4+2N.
func TestDatabaseG32CComparisonQueryScaling(t *testing.T) {
	ctx := context.Background()
	got := map[int][2]int64{}
	for _, n := range []int{1, 10, 50, 200} {
		w := g32cBuild(t, fmt.Sprintf("合成查询规模%03d", n), g32cSpec{readable: n - n/2, hidden: n / 2, legacy: 1})
		tr := newTracedApp(t, w.f.db)
		dir := login(t, tr.srv, "synthetic-director")
		p, err := get(ctx, tr.pool, w.primary.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		cq := tr.count(t, func() {
			if cs, err := tr.app.comparison(ctx, tr.pool, p, w.users["director"]); err != nil || len(cs) != n {
				t.Fatal(err, len(cs))
			}
		})
		hq := tr.count(t, func() {
			s, b := dir.call("GET", "/api/v1/projects/"+p.ID+"/duplicate-comparison", nil)
			expect(t, s, 200, b)
		})
		got[n] = [2]int64{cq, hq}
		t.Logf("COMPARISON_QUERIES candidates=%d comparison=%d http=%d", n, cq, hq)
	}
	for _, n := range []int{10, 50, 200} {
		if got[n] != got[1] {
			t.Fatalf("query count grows with candidates: %v", got)
		}
	}
	if got[1][0] != 1 || got[1][1] > 8 {
		t.Fatalf("comparison()=%d (want 1), HTTP=%d (want <=8)", got[1][0], got[1][1])
	}
}
