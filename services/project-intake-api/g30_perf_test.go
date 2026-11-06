package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// G3-0 performance baseline for duplicate comparison. Measurement only: it
// records query counts, timings and plans for 1/10/50 candidates and asserts
// nothing about speed. Run with BAOBEI_PERF_REPORT=<file> to write a report.
func TestDatabaseG30ComparisonBaseline(t *testing.T) {
	report := os.Getenv("BAOBEI_PERF_REPORT")
	if report == "" {
		t.Skip("NOT_RUN: set BAOBEI_PERF_REPORT to measure")
	}
	ctx := context.Background()
	var out strings.Builder
	// G3-1 correction: the earlier "detail" column measured app.projectHints()
	// only. The full GET /api/v1/projects/{id} endpoint is measured separately
	// through real HTTP (session check, detail, activities, audit, governance).
	fmt.Fprintf(&out, "| 候选数 | comparison() 查询数 | 中位 | projectHints() 查询数 | 中位 | GET /projects/{id} 查询数 | P50 | P95 | GET duplicate-comparison 查询数 | P50 | P95 |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	var plans strings.Builder
	for _, n := range []int{1, 10, 50} {
		f := g1FixtureFor(t)
		name := fmt.Sprintf("合成性能候选%d", n)
		// n submitted candidates through the real API (same name and customer).
		for i := 0; i < n; i++ {
			g1Submit(t, f.sales, g1Create(t, f.sales, name))
		}
		// Submitting freezes revision 1 and records real duplicate cases for
		// every similar approved project, so the fixture uses the normal path.
		p := g1Submit(t, f.other, g1Create(t, f.other, name))
		var got int
		_ = f.db.QueryRow(ctx, `SELECT count(*) FROM duplicate_cases WHERE project_id=$1`, p.ID).Scan(&got)
		if got != n {
			t.Fatal("fixture", got, n)
		}
		var counter atomic.Int64
		cfg := f.db.Config().Copy()
		cfg.ConnConfig.Tracer = recordingTracer{&counter}
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		u := User{Subject: "synthetic-director", Role: directorRole}
		app := &App{db: pool}
		proj, err := get(ctx, pool, p.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		measureN := func(runs int, fn func() error) (int64, time.Duration, time.Duration) {
			var ds []time.Duration
			var q int64
			for i := 0; i < runs; i++ {
				counter.Store(0)
				st := time.Now()
				if err := fn(); err != nil {
					t.Fatal(err)
				}
				ds = append(ds, time.Since(st))
				q = counter.Load()
			}
			sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
			return q, ds[len(ds)/2], ds[(len(ds)*95+99)/100-1]
		}
		measure := func(fn func() error) (int64, time.Duration) { q, p50, _ := measureN(7, fn); return q, p50 }
		cq, ct := measure(func() error { _, e := app.comparison(ctx, pool, proj, u); return e })
		dq, dt := measure(func() error { x := proj; return app.projectHints(ctx, pool, u, &x) })
		// Full HTTP endpoints on the traced pool, 40 runs each for P50/P95.
		httpApp := &App{db: pool, cfg: Config{Env: "development", Dev: true}}
		srv := httptest.NewServer(httpApp.handler())
		httpApp.cfg.Origin = srv.URL
		dir := login(t, srv, "synthetic-director")
		get := func(path string) func() error {
			return func() error {
				if s, b := dir.call("GET", path, nil); s != 200 {
					return fmt.Errorf("%s %d %s", path, s, b)
				}
				return nil
			}
		}
		hq, h50, h95 := measureN(40, get("/api/v1/projects/"+p.ID))
		xq, x50, x95 := measureN(40, get("/api/v1/projects/"+p.ID+"/duplicate-comparison"))
		srv.Close()
		r := func(d time.Duration) time.Duration { return d.Round(10 * time.Microsecond) }
		fmt.Fprintf(&out, "| %d | %d | %s | %d | %s | %d | %s | %s | %d | %s | %s |\n", n, cq, r(ct), dq, r(dt), hq, r(h50), r(h95), xq, r(x50), r(x95))
		if n == 50 {
			for _, q := range []struct{ title, sql string }{
				{"comparison 主查询", `EXPLAIN (ANALYZE, BUFFERS) SELECT id::text,candidate_id::text,score,risk,matched_signals,decision FROM duplicate_cases WHERE project_id='` + p.ID + `' AND (submission_revision=1 OR decision<>'PENDING') ORDER BY created_at DESC,id`},
				{"comparison 逐候选 get()", `EXPLAIN (ANALYZE, BUFFERS) SELECT ` + columns + ` FROM intake_projects p WHERE p.id=(SELECT candidate_id FROM duplicate_cases WHERE project_id='` + p.ID + `' LIMIT 1)`},
				{"comparison 逐候选 evidenceGrade()", `EXPLAIN (ANALYZE, BUFFERS) SELECT ` + primaryCompleteSQL("p") + ` FROM intake_projects p WHERE p.id=(SELECT candidate_id FROM duplicate_cases WHERE project_id='` + p.ID + `' LIMIT 1)`},
				{"审核资格事实（单项目）", `EXPLAIN (ANALYZE, BUFFERS) SELECT p.id::text,` + strings.NewReplacer("$1", "'synthetic-director'", "$2", "'baobei_director'", "$3", "''").Replace(primaryConflictSQL("p", "$1")+`,`+primaryCompleteSQL("p")+`,EXISTS(`+relatedCandidatesSQL("p")+` AND NOT `+projectScope("c")+`),EXISTS(`+relatedCandidatesSQL("p")+` AND `+candidateConflictSQL("c", "$1")+`)`) + ` FROM intake_projects p WHERE p.id='` + p.ID + `'`},
			} {
				rows, err := f.db.Query(ctx, q.sql)
				if err != nil {
					t.Fatal(q.title, err)
				}
				fmt.Fprintf(&plans, "\n### %s（50 个候选）\n\n```text\n", q.title)
				for rows.Next() {
					var line string
					_ = rows.Scan(&line)
					plans.WriteString(line + "\n")
				}
				rows.Close()
				plans.WriteString("```\n")
			}
		}
		pool.Close()
	}
	t.Log("\n" + out.String())
	if err := os.WriteFile(report, []byte(out.String()+plans.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

type recordingTracer struct{ n *atomic.Int64 }

func (r recordingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	r.n.Add(1)
	return ctx
}
func (recordingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
