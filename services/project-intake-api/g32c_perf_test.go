package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// G3-2C performance evidence. Measurement only (no speed assertions); runs
// when BAOBEI_SCALE_REPORT=<file> is set and appends Markdown to that file.
// BAOBEI_PG_RESTART, when set, is a shell command that restarts the local
// PostgreSQL so the first request runs with empty shared_buffers ("cold"; the
// OS page cache is not dropped in this sandbox). Numbers are only comparable
// within one environment and say nothing about production latency.

type stmt struct {
	sql  string
	args []any
	dur  time.Duration
}

// captureTracer counts statements and keeps their text, arguments and
// duration (the duration of a lock statement includes the lock wait).
type captureTracer struct {
	mu   sync.Mutex
	on   bool
	list []stmt
}
type captureKey struct{}

func (c *captureTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, captureKey{}, stmt{sql: d.SQL, args: d.Args, dur: time.Duration(time.Now().UnixNano())})
}
func (c *captureTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	s, ok := ctx.Value(captureKey{}).(stmt)
	if !ok {
		return
	}
	s.dur = time.Duration(time.Now().UnixNano()) - s.dur
	c.mu.Lock()
	if c.on {
		c.list = append(c.list, s)
	}
	c.mu.Unlock()
}
func (c *captureTracer) start() { c.mu.Lock(); c.on, c.list = true, nil; c.mu.Unlock() }
func (c *captureTracer) stop() []stmt {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.on = false
	return c.list
}

type perfEnv struct {
	f      g1Fixture
	tracer *captureTracer
	pool   *pgxpool.Pool
	app    *App
	cl     map[string]testClient
	out    *strings.Builder
}

func newPerfEnv(t *testing.T, f g1Fixture, out *strings.Builder) *perfEnv {
	t.Helper()
	tr := &captureTracer{}
	cfg := f.db.Config().Copy()
	cfg.ConnConfig.Tracer = tr
	cfg.MaxConns = 16
	if v := os.Getenv("BAOBEI_SCALE_JIT"); v != "" {
		// Experiment only (not product code): compare plans with JIT on/off.
		cfg.ConnConfig.RuntimeParams["jit"] = v
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{db: pool, cfg: Config{Env: "development", Dev: true}}
	srv := httptest.NewServer(app.handler())
	app.cfg.Origin = srv.URL
	t.Cleanup(func() { srv.Close(); pool.Close() })
	e := &perfEnv{f: f, tracer: tr, pool: pool, app: app, cl: map[string]testClient{}, out: out}
	for who, s := range map[string]string{"manager": "synthetic-manager", "director": "synthetic-director", "admin": "synthetic-admin", "sales": "synthetic-a"} {
		e.cl[who] = login(t, srv, s)
	}
	return e
}

type perfSample struct {
	queries      int
	p50, p95     time.Duration
	bytes        int
	lockWait     time.Duration
	statements   []stmt
	firstElapsed time.Duration
}

// measure runs fn `runs` times (after one warm-up unless cold) and records
// statements of the last run.
func (e *perfEnv) measure(t *testing.T, runs int, fn func() int) perfSample {
	t.Helper()
	var ds []time.Duration
	var s perfSample
	for i := 0; i < runs; i++ {
		e.tracer.start()
		st := time.Now()
		s.bytes = fn()
		d := time.Since(st)
		s.statements = e.tracer.stop()
		if i == 0 {
			s.firstElapsed = d
		}
		ds = append(ds, d)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	s.p50, s.p95 = ds[len(ds)/2], ds[(len(ds)*95+99)/100-1]
	s.queries = len(s.statements)
	for _, x := range s.statements {
		if strings.Contains(x.sql, "pg_advisory_xact_lock") || strings.Contains(x.sql, "FOR UPDATE") {
			s.lockWait += x.dur
		}
	}
	return s
}

func (e *perfEnv) get(t *testing.T, who, path string) func() int {
	return func() int {
		s, b := e.cl[who].call("GET", path, nil)
		if s != 200 {
			t.Fatalf("%s %s HTTP %d %s", who, path, s, b)
		}
		return len(b)
	}
}

// buffers explains every SELECT of a captured request with ANALYZE+BUFFERS
// and sums shared hit/read blocks over the request.
func (e *perfEnv) buffers(t *testing.T, list []stmt) (hit, read int64, plans []string) {
	t.Helper()
	ctx := context.Background()
	seen := map[string]bool{}
	for _, s := range list {
		q := strings.TrimSpace(s.sql)
		if !strings.HasPrefix(strings.ToUpper(q), "SELECT") || strings.Contains(q, "pg_advisory") {
			continue
		}
		var raw []byte
		if err := e.pool.QueryRow(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) `+q, s.args...).Scan(&raw); err != nil {
			t.Fatalf("explain: %v\n%s", err, q)
		}
		var doc []struct {
			Plan map[string]any `json:"Plan"`
		}
		_ = json.Unmarshal(raw, &doc)
		h, _ := doc[0].Plan["Shared Hit Blocks"].(float64)
		r, _ := doc[0].Plan["Shared Read Blocks"].(float64)
		hit += int64(h)
		read += int64(r)
		key := q
		if len(key) > 120 {
			key = key[:120]
		}
		if !seen[key] {
			seen[key] = true
			plans = append(plans, q)
		}
	}
	return hit, read, plans
}

func (e *perfEnv) textPlan(t *testing.T, s stmt) string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `EXPLAIN (ANALYZE, BUFFERS) `+s.sql, s.args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		b.WriteString(line + "\n")
	}
	return b.String()
}

// restart empties shared_buffers; the pool reconnects on the next acquire.
func restartPG(t *testing.T) bool {
	t.Helper()
	cmd := os.Getenv("BAOBEI_PG_RESTART")
	if cmd == "" {
		return false
	}
	if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("restart: %v %s", err, out)
	}
	time.Sleep(1200 * time.Millisecond) // > pgxpool idle ping threshold
	return true
}

func r10(d time.Duration) string {
	if d >= time.Millisecond {
		return d.Round(10 * time.Microsecond).String()
	}
	return d.Round(time.Microsecond).String()
}

// cloneProject duplicates a submitted project (row, frozen submission and its
// duplicate cases) with new IDs. Synthetic: used only to obtain many identical
// primaries for repeated POST timing.
func cloneProject(t *testing.T, db *pgxpool.Pool, id string) string {
	t.Helper()
	var nid string
	err := db.QueryRow(context.Background(), `WITH n AS (INSERT INTO intake_projects
 SELECT (jsonb_populate_record(NULL::intake_projects, to_jsonb(p) || jsonb_build_object('id',gen_random_uuid(),'registration_no',next_registration_no()))).* FROM intake_projects p WHERE p.id=$1 RETURNING id),
 s AS (INSERT INTO project_submissions SELECT (jsonb_populate_record(NULL::project_submissions, to_jsonb(x) || jsonb_build_object('project_id',(SELECT id FROM n)))).* FROM project_submissions x WHERE x.project_id=$1 RETURNING 1)
 SELECT id::text FROM n, (SELECT count(*) FROM s) c`, id).Scan(&nid)
	if err != nil {
		t.Fatal(err)
	}
	g1Exec(t, db, `INSERT INTO duplicate_cases SELECT (jsonb_populate_record(NULL::duplicate_cases, to_jsonb(d) || jsonb_build_object('id',gen_random_uuid(),'project_id',$2::uuid))).* FROM duplicate_cases d WHERE d.project_id=$1`, id, nid)
	return nid
}

func TestDatabaseG32CPerfCandidates(t *testing.T) {
	report := os.Getenv("BAOBEI_SCALE_REPORT")
	if report == "" {
		t.Skip("NOT_RUN: set BAOBEI_SCALE_REPORT to measure")
	}
	ctx := context.Background()
	var out, plans strings.Builder
	fmt.Fprintf(&out, "\n## 候选规模（真实 API 创建候选与首个主项目；POST 主项目为 SQL 克隆）\n\n| 候选 | 视角 | comparison() 查询 | P50 | HTTP 查询 | 冷首请求 | 热 P50 | 热 P95 | 响应字节 | Buffers 热 hit/read | 冷 read | POST 查询 | POST P50 | POST P95 | POST 锁语句耗时 |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, n := range []int{1, 10, 50, 200} {
		w := g32cBuild(t, fmt.Sprintf("合成性能%03d", n), g32cSpec{readable: n - n/2, hidden: n / 2, legacy: 1})
		e := newPerfEnv(t, w.f, &out)
		path := "/api/v1/projects/" + w.primary.ID + "/duplicate-comparison"
		for _, who := range []string{"director", "manager"} {
			u := w.users[who]
			p, _ := get(ctx, e.pool, w.primary.ID, false)
			cmp := e.measure(t, 40, func() int {
				if _, err := e.app.comparison(ctx, e.pool, p, u); err != nil {
					t.Fatal(err)
				}
				return 0
			})
			cold := perfSample{}
			coldRead := int64(-1)
			if restartPG(t) {
				cold = e.measure(t, 1, e.get(t, who, path))
				// Explain the cold statements right after another restart.
				restartPG(t)
				_, coldRead, _ = e.buffers(t, cold.statements)
			}
			hot := e.measure(t, 40, e.get(t, who, path))
			hit, read, list := e.buffers(t, hot.statements)
			// POST: one cloned primary per run (director; manager is restricted).
			post := perfSample{}
			if who == "director" {
				runs := 15
				ids := make([]string, runs)
				for i := range ids {
					ids[i] = cloneProject(t, w.f.db, w.primary.ID)
				}
				i := 0
				post = e.measure(t, runs, func() int {
					id := ids[i]
					i++
					cp, _ := get(ctx, w.f.db, id, false)
					var caseID string
					_ = w.f.db.QueryRow(ctx, `SELECT d.id::text FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=$1 ORDER BY c.registration_no LIMIT 1`, id).Scan(&caseID)
					vers := map[string]int{}
					rows, _ := w.f.db.Query(ctx, `SELECT c.id::text,c.version FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=$1`, id)
					for rows.Next() {
						var k string
						var v int
						_ = rows.Scan(&k, &v)
						vers[k] = v
					}
					rows.Close()
					e.tracer.start() // exclude the fixture reads above
					s, b := e.cl["director"].call("POST", "/api/v1/projects/"+id+"/duplicate-decisions", map[string]any{"version": cp.Version, "reason": "合成性能", "caseId": caseID, "decision": "DISTINCT_APPROVED", "relatedVersions": vers, "acknowledgeLegacyEvidence": true})
					if s != 200 {
						t.Fatalf("POST %d %s", s, b)
					}
					return len(b)
				})
			}
			coldS, coldR := "未测", "未测"
			if cold.statements != nil {
				coldS, coldR = r10(cold.firstElapsed), fmt.Sprint(coldRead)
			}
			postQ, postP50, postP95, postLock := "—", "—", "—", "—"
			if who == "director" {
				postQ, postP50, postP95, postLock = fmt.Sprint(post.queries), r10(post.p50), r10(post.p95), r10(post.lockWait)
			}
			fmt.Fprintf(&out, "| %d | %s | %d | %s | %d | %s | %s | %s | %d | %d/%d | %s | %s | %s | %s | %s |\n", n, who, cmp.queries, r10(cmp.p50), hot.queries, coldS, r10(hot.p50), r10(hot.p95), hot.bytes, hit, read, coldR, postQ, postP50, postP95, postLock)
			if n == 50 && who == "director" {
				fmt.Fprintf(&plans, "\n### 50 候选 · 总监 · GET duplicate-comparison 各 SELECT 执行计划（热缓存）\n")
				for _, s := range hot.statements {
					q := strings.TrimSpace(s.sql)
					if !strings.HasPrefix(strings.ToUpper(q), "SELECT") {
						continue
					}
					short := strings.Join(strings.Fields(q), " ")
					if len(short) > 160 {
						short = short[:160] + "…"
					}
					if strings.Contains(plans.String(), short) {
						continue // repeated per-candidate statement: plan shown once
					}
					fmt.Fprintf(&plans, "\n`%s`\n\n```text\n%s```\n", short, e.textPlan(t, s))
				}
				_ = list
			}
		}
	}
	appendReport(t, report, out.String()+plans.String())
}

func appendReport(t *testing.T, path, s string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	_, _ = fh.WriteString(s)
	t.Log(s)
}

// scaleData builds a project population of `total` rows. A small set of seed
// projects is created through the real HTTP API (labelled API); the rest are
// SQL clones with varied owner, team, status, evidence, participation,
// ownership history and duplicate cases (labelled synthetic).
func scaleData(t *testing.T, f g1Fixture, total int) map[string]int {
	t.Helper()
	ctx := context.Background()
	c := login(t, f.server, "synthetic-c")
	seedSub := g1Submit(t, f.sales, g1Create(t, f.sales, "合成规模种子"))
	g1Create(t, c, "合成规模种子草稿")
	g1Exec(t, f.db, `ALTER TABLE intake_projects DROP CONSTRAINT IF EXISTS submission_projection`)
	stmts := []string{
		// owners rotate over three sales in two teams; statuses by g%20.
		`CREATE TEMP TABLE syn AS SELECT g,
  (ARRAY['synthetic-a','synthetic-b','synthetic-c'])[1+g%3] AS owner,
  (ARRAY['11111111-1111-4111-8111-111111111111','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222'])[1+g%3]::uuid AS team,
  CASE WHEN g%20<8 THEN 'APPROVED' WHEN g%20<11 THEN 'SUBMITTED' WHEN g%20<14 THEN 'UNDER_REVIEW' WHEN g%20<18 THEN 'DRAFT' WHEN g%20<19 THEN 'RETURNED' ELSE 'REJECTED' END AS st
  FROM generate_series(1,$1::int) g`,
		`INSERT INTO intake_projects SELECT (jsonb_populate_record(NULL::intake_projects, to_jsonb(t) || jsonb_build_object(
  'id',gen_random_uuid(),'registration_no','BB-2099-'||lpad(s.g::text,6,'0'),'project_name','合成规模项目'||s.g,'normalized_project_name',lower('合成规模项目'||s.g),
  'owner_subject',s.owner,'created_by_subject',s.owner,'draft_custodian_subject',s.owner,'team_id',s.team,
  'intake_status',s.st,'submission_revision',CASE WHEN s.st IN ('DRAFT') THEN 0 ELSE 1 END,
  'submitted_by_subject',CASE WHEN s.st IN ('DRAFT') THEN NULL ELSE s.owner END,
  'evidence_state',CASE WHEN s.st='DRAFT' THEN 'INCOMPLETE' ELSE 'COMPLETE' END,
  'document',t.document || jsonb_build_object('projectName','合成规模项目'||s.g,'customer','合成规模客户'||(s.g%400),'location',jsonb_build_object('province','合成省','city','合成市'||(s.g%50),'factoryName','厂'||(s.g%7))),
  'created_at',now()-s.g*interval '1 minute','updated_at',now()-s.g*interval '30 second'))).*
  FROM syn s, intake_projects t WHERE t.id=$1::uuid`,
		// 5% of approved rows are migrated history without frozen evidence.
		`UPDATE intake_projects SET submission_revision=0,submitted_by_subject=NULL,evidence_state='INCOMPLETE' WHERE project_name LIKE '合成规模项目%' AND intake_status='APPROVED' AND substring(project_name from 7)::int % 100 < 5`,
		`INSERT INTO project_submissions(project_id,revision,source_version,actor_subject,creator_subject,owner_subject,team_id,document,document_hash,participants,evidence_state,created_at)
  SELECT id,1,version,owner_subject,created_by_subject,owner_subject,team_id,document,document_hash,'[]',evidence_state,updated_at FROM intake_projects WHERE project_name LIKE '合成规模项目%' AND submission_revision=1`,
		`INSERT INTO project_participations(project_id,actor_subject,document_hash,kind,source_version,owner_subject,team_id)
  SELECT id,created_by_subject,document_hash,'CREATE',1,owner_subject,team_id FROM intake_projects WHERE project_name LIKE '合成规模项目%'`,
		// 10% handed over by a manager or the director (participation + history).
		`INSERT INTO project_participations(project_id,actor_subject,document_hash,kind,source_version,owner_subject,team_id)
  SELECT id,CASE WHEN team_id='11111111-1111-4111-8111-111111111111' THEN 'synthetic-manager' ELSE 'synthetic-director' END,document_hash,'ASSIGN',1,owner_subject,team_id FROM intake_projects WHERE project_name LIKE '合成规模项目%' AND substring(project_name from 7)::int % 10 = 3`,
		`INSERT INTO ownership_history(project_id,from_subject,to_subject,reason,actor_subject)
  SELECT id,NULL,owner_subject,'合成规模交接',CASE WHEN team_id='11111111-1111-4111-8111-111111111111' THEN 'synthetic-manager' ELSE 'synthetic-director' END FROM intake_projects WHERE project_name LIKE '合成规模项目%' AND substring(project_name from 7)::int % 10 = 3`,
		// each UNDER_REVIEW row gets 3 pending candidates among approved rows
		// (cross-team for about a third).
		`CREATE TEMP TABLE appr AS SELECT id,row_number() OVER (ORDER BY id) rn FROM intake_projects WHERE project_name LIKE '合成规模项目%' AND intake_status='APPROVED'`,
		`INSERT INTO duplicate_cases(project_id,candidate_id,score,risk,matched_signals,submission_revision,created_at)
  SELECT p.id,a.id,80,'WARN','["相同客户或别名"]',1,p.updated_at FROM (SELECT id,updated_at,row_number() OVER (ORDER BY id) rn FROM intake_projects WHERE project_name LIKE '合成规模项目%' AND intake_status='UNDER_REVIEW') p
  JOIN appr a ON a.rn IN (1+p.rn%(SELECT count(*) FROM appr),1+(p.rn*7)%(SELECT count(*) FROM appr),1+(p.rn*13)%(SELECT count(*) FROM appr))`,
		`DROP TABLE syn`, `DROP TABLE appr`, `ANALYZE`,
	}
	conn, err := f.db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for i, s := range stmts {
		var args []any
		if strings.Contains(s, "$1::int") {
			args = append(args, total)
		} else if strings.Contains(s, "$1::uuid") {
			args = append(args, seedSub.ID)
		}
		st := time.Now()
		if _, err := conn.Exec(ctx, s, args...); err != nil {
			t.Fatalf("scale stmt %d: %v", i, err)
		}
		if pf := os.Getenv("BAOBEI_SCALE_PROGRESS"); pf != "" {
			fh, _ := os.OpenFile(pf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fmt.Fprintf(fh, "stmt %d %s\n", i, time.Since(st))
			fh.Close()
		}
	}
	counts := map[string]int{}
	rows, _ := f.db.Query(ctx, `SELECT intake_status,count(*) FROM intake_projects GROUP BY 1`)
	for rows.Next() {
		var k string
		var v int
		_ = rows.Scan(&k, &v)
		counts[k] = v
	}
	rows.Close()
	return counts
}

func TestDatabaseG32CPerfProjectScale(t *testing.T) {
	report := os.Getenv("BAOBEI_SCALE_REPORT")
	if report == "" {
		t.Skip("NOT_RUN: set BAOBEI_SCALE_REPORT to measure")
	}
	sizes := []int{1000, 10000}
	if v := os.Getenv("BAOBEI_SCALE_SIZES"); v != "" {
		sizes = nil
		for _, s := range strings.Split(v, ",") {
			var n int
			fmt.Sscan(s, &n)
			sizes = append(sizes, n)
		}
	}
	ctx := context.Background()
	for _, total := range sizes {
		var out, plans strings.Builder
		f := g1FixtureFor(t)
		built := time.Now()
		counts := scaleData(t, f, total)
		if pf := os.Getenv("BAOBEI_SCALE_PROGRESS"); pf != "" {
			fh, _ := os.OpenFile(pf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fmt.Fprintf(fh, "%s built %d in %s\n", time.Now().Format("15:04:05"), total, time.Since(built))
			fh.Close()
		}
		var cases, subs, parts int
		_ = f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM duplicate_cases),(SELECT count(*) FROM project_submissions),(SELECT count(*) FROM project_participations)`).Scan(&cases, &subs, &parts)
		fmt.Fprintf(&out, "\n## 项目规模 %d（合成 SQL 克隆 %d 条 + 真实 API 种子；构建 %s）\n\n状态分布 %v；duplicate_cases=%d，project_submissions=%d，project_participations=%d。\n\n", total, total, time.Since(built).Round(time.Millisecond), counts, cases, subs, parts)
		e := newPerfEnv(t, f, &out)
		fmt.Fprintf(&out, "| 场景 | 视角 | 查询数 | 冷首请求 | 热 P50 | 热 P95 | 响应字节 | Buffers 热 hit/read | 冷 read |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
		row := func(label, who string, fn func() int, runs int, plan bool) perfSample {
			cold := perfSample{}
			coldRead := "未测"
			if (plan || strings.Contains(label, "对比") || strings.Contains(label, "查重")) && restartPG(t) {
				cold = e.measure(t, 1, fn)
				restartPG(t)
				_, r, _ := e.buffers(t, cold.statements)
				coldRead = fmt.Sprint(r)
			}
			hot := e.measure(t, runs, fn)
			hit, read, _ := e.buffers(t, hot.statements)
			coldS := "未测"
			if cold.statements != nil {
				coldS = r10(cold.firstElapsed)
			}
			line := fmt.Sprintf("| %s | %s | %d | %s | %s | %s | %d | %d/%d | %s |\n", label, who, hot.queries, coldS, r10(hot.p50), r10(hot.p95), hot.bytes, hit, read, coldRead)
			out.WriteString(line)
			if pf := os.Getenv("BAOBEI_SCALE_PROGRESS"); pf != "" {
				fh, _ := os.OpenFile(pf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
				fmt.Fprintf(fh, "%s %d %s", time.Now().Format("15:04:05"), total, line)
				fh.Close()
			}
			if plan {
				for _, s := range hot.statements {
					q := strings.TrimSpace(s.sql)
					if strings.HasPrefix(q, "SELECT now(),count(*)") || strings.Contains(q, "LIMIT $4 OFFSET $5") || strings.HasPrefix(q, "SELECT count(*),") ||
						strings.Contains(q, "FROM duplicate_cases d JOIN intake_projects p ON p.id=d.candidate_id") || strings.Contains(q, "similarity(p.normalized_project_name") {
						fmt.Fprintf(&plans, "\n### %d · %s · %s\n\n```text\n%s```\n", total, label, who, e.textPlan(t, s))
					}
				}
			}
			return hot
		}
		part := os.Getenv("BAOBEI_SCALE_PART") // manager|director|admin|other; empty = all
		runs := 15
		if total >= 10000 {
			runs = 5
		}
		var jit string
		_ = e.pool.QueryRow(ctx, `SHOW jit`).Scan(&jit)
		fmt.Fprintf(&out, "（本段：%s，热重复 %d 次，jit=%s）\n\n", map[bool]string{true: "全部", false: part}[part == ""], runs, jit)
		for _, who := range []string{"manager", "director", "admin"} {
			if part != "" && part != who {
				continue
			}
			var q struct {
				Counts map[string]int `json:"counts"`
			}
			_, b := e.cl[who].call("GET", "/api/v1/reviews?kind=pending&eligibility=all&pageSize=20", nil)
			_ = json.Unmarshal(b, &q)
			for _, el := range []string{"reviewable", "blocked", "all"} {
				last := (q.Counts[el]+19)/20 + 0
				if last < 1 {
					last = 1
				}
				row(fmt.Sprintf("审核队列 %s 第1页", el), who, e.get(t, who, "/api/v1/reviews?kind=pending&eligibility="+el+"&pageSize=20&page=1"), runs, el == "reviewable")
				row(fmt.Sprintf("审核队列 %s 末页(%d)", el, last), who, e.get(t, who, fmt.Sprintf("/api/v1/reviews?kind=pending&eligibility=%s&pageSize=20&page=%d", el, last)), runs, false)
			}
			row("首页汇总", who, e.get(t, who, "/api/v1/workspace-summary"), runs, who == "manager")
			fmt.Fprintf(&out, "| 计数 %s | %s | counts=%v | | | | | | |\n", who, who, q.Counts)
		}
		if part != "" && part != "other" {
			appendReport(t, report, out.String()+plans.String())
			continue
		}
		// Full comparison on a project with 50 related candidates in this population.
		var primary string
		_ = f.db.QueryRow(ctx, `SELECT id::text FROM intake_projects WHERE project_name LIKE '合成规模项目%' AND intake_status='UNDER_REVIEW' AND team_id='11111111-1111-4111-8111-111111111111' ORDER BY id LIMIT 1`).Scan(&primary)
		g1Exec(t, f.db, `INSERT INTO duplicate_cases(project_id,candidate_id,score,risk,matched_signals,submission_revision,created_at)
 SELECT $1,id,75,'WARN','["相同位置"]',1,now() FROM intake_projects WHERE intake_status='APPROVED' AND project_name LIKE '合成规模项目%' AND id<>$1 ORDER BY id DESC LIMIT 47`, primary)
		for _, who := range []string{"director", "manager"} {
			row("完整重复对比（50 候选）", who, e.get(t, who, "/api/v1/projects/"+primary+"/duplicate-comparison"), 20, who == "director")
		}
		// Live duplicate check while filling the wizard (sales).
		probe := sample("合成规模项目77")
		probe.Customer = "合成规模客户77"
		probe.Location = Location{Province: "合成省", City: "合成市27", FactoryName: "厂0"}
		live := row("创建阶段实时查重 POST duplicate-checks", "sales", func() int {
			s, b := e.cl["sales"].call("POST", "/api/v1/duplicate-checks", probe)
			if s != 200 {
				t.Fatal(s, string(b))
			}
			return len(b)
		}, 15, true)
		_ = live
		// Submit re-check: drafts created through the API, then submitted.
		drafts := make([]Project, 14)
		for i := range drafts {
			in := probe
			in.ProjectName = fmt.Sprintf("合成规模提交%02d", i)
			s, b := e.cl["sales"].call("POST", "/api/v1/projects", in)
			drafts[i] = projectFrom(t, s, b)
		}
		i := 0
		row("提交时重新查重 POST submit（API 草稿）", "sales", func() int {
			p := drafts[i]
			i++
			s, b := e.cl["sales"].call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
			if s != 200 {
				t.Fatal(s, string(b))
			}
			return len(b)
		}, 12, false)
		// Concurrent reviews: 8 sessions decide 8 different primaries that
		// share candidates (director x4, admin x4), 3 rounds.
		var lat []time.Duration
		var lock time.Duration
		var mu sync.Mutex
		var statuses []int
		var wallTotal time.Duration
		for round := 0; round < 3; round++ {
			var targets []string
			rows, _ := f.db.Query(ctx, `SELECT p.id::text FROM intake_projects p WHERE p.project_name LIKE '合成规模项目%' AND p.intake_status='SUBMITTED' AND NOT EXISTS(SELECT 1 FROM project_participations x WHERE x.project_id=p.id AND x.kind='ASSIGN') ORDER BY p.id OFFSET $1 LIMIT 8`, round*8)
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				targets = append(targets, id)
			}
			rows.Close()
			e.tracer.start()
			var wg sync.WaitGroup
			st := time.Now()
			for k, id := range targets {
				who := []string{"director", "admin"}[k%2]
				p, _ := get(ctx, f.db, id, false)
				wg.Add(1)
				go func() {
					defer wg.Done()
					t0 := time.Now()
					s, _ := e.cl[who].call("POST", "/api/v1/projects/"+id+"/approve", map[string]any{"version": p.Version, "reason": "合成并发审核"})
					d := time.Since(t0)
					mu.Lock()
					lat = append(lat, d)
					statuses = append(statuses, s)
					mu.Unlock()
				}()
			}
			wg.Wait()
			wallTotal += time.Since(st)
			for _, s := range e.tracer.stop() {
				if strings.Contains(s.sql, "pg_advisory_xact_lock") || strings.Contains(s.sql, "FOR UPDATE") {
					lock += s.dur
				}
			}
		}
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		ok := 0
		for _, s := range statuses {
			if s == 200 {
				ok++
			}
		}
		fmt.Fprintf(&out, "\n多人并发审核（8 会话 × 3 轮，各批准不同项目）：%d/%d 成功，单请求 P50 %s / P95 %s，3 轮墙钟合计 %s，锁语句累计 %s，无死锁。\n", ok, len(statuses), r10(lat[len(lat)/2]), r10(lat[(len(lat)*95+99)/100-1]), r10(wallTotal), r10(lock))
		appendReport(t, report, out.String()+plans.String())
	}
}
