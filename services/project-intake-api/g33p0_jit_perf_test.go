package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// G3-3-P0 JIT measurement. Runs only with BAOBEI_JIT_REPORT=<file>; needs a
// server started with shared_preload_libraries=pg_stat_statements so the JIT
// work of the statements the API really executed (pgx prepared statements,
// inside the real transactions) is read from pg_stat_statements instead of
// being re-planned by EXPLAIN. Measurement only; nothing is asserted about
// speed. Optional: BAOBEI_SCALE_JIT=off|on sets the session default for the
// whole pool (experiment), BAOBEI_PG_RESTART enables cold first requests.

type jitStats struct {
	calls                              int64
	functions                          int64
	gen, inline, opt, emit, exec, plan float64
	statements                         map[string]int64
}

func (j jitStats) jitTotal() float64 { return j.gen + j.inline + j.opt + j.emit }

func statStatementsReady(t *testing.T, e *perfEnv) bool {
	t.Helper()
	ctx := context.Background()
	var preload string
	_ = e.pool.QueryRow(ctx, `SHOW shared_preload_libraries`).Scan(&preload)
	if !strings.Contains(preload, "pg_stat_statements") {
		return false
	}
	if _, err := e.pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	return true
}

func resetStatements(t *testing.T, e *perfEnv) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `SELECT public.pg_stat_statements_reset()`); err != nil {
		t.Fatal(err)
	}
}

// readStatements sums every statement except the harness' own reset/read.
func readStatements(t *testing.T, e *perfEnv) jitStats {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT query,calls,jit_functions,jit_generation_time,jit_inlining_time,jit_optimization_time,jit_emission_time,total_exec_time,total_plan_time
 FROM public.pg_stat_statements WHERE query NOT LIKE '%pg_stat_statements%' AND query NOT LIKE 'SHOW %'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	s := jitStats{statements: map[string]int64{}}
	for rows.Next() {
		var q string
		var calls, fn int64
		var g, i, o, em, ex, pl float64
		if err = rows.Scan(&q, &calls, &fn, &g, &i, &o, &em, &ex, &pl); err != nil {
			t.Fatal(err)
		}
		s.calls += calls
		s.functions += fn
		s.gen, s.inline, s.opt, s.emit, s.exec, s.plan = s.gen+g, s.inline+i, s.opt+o, s.emit+em, s.exec+ex, s.plan+pl
		k := strings.Join(strings.Fields(q), " ")
		if len(k) > 70 {
			k = k[:70]
		}
		if fn > 0 {
			k += " [JIT]"
		}
		s.statements[k] += calls
	}
	return s
}

// backendCPU returns utime+stime (clock ticks) summed over all PostgreSQL
// processes visible to this test, and the largest VmHWM (kB) of a backend.
func backendCPU() (ticks int64, hwm int64) {
	paths, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || !strings.HasPrefix(string(b), "postgres") {
			continue
		}
		dir := filepath.Dir(p)
		if st, err := os.ReadFile(dir + "/stat"); err == nil {
			f := strings.Fields(string(st[strings.LastIndexByte(string(st), ')')+2:]))
			u, _ := strconv.ParseInt(f[11], 10, 64)
			s, _ := strconv.ParseInt(f[12], 10, 64)
			ticks += u + s
		}
		if strings.HasPrefix(string(b), "postgres: ") {
			if st, err := os.ReadFile(dir + "/status"); err == nil {
				for _, line := range strings.Split(string(st), "\n") {
					if strings.HasPrefix(line, "VmHWM:") {
						v, _ := strconv.ParseInt(strings.Fields(line)[1], 10, 64)
						if v > hwm {
							hwm = v
						}
					}
				}
			}
		}
	}
	return
}

// replayPlans re-runs the captured statements of one request in a rolled
// back transaction, honouring captured SET LOCAL statements, and returns the
// EXPLAIN (ANALYZE, BUFFERS) text of every SELECT.
func replayPlans(t *testing.T, e *perfEnv, list []stmt) []string {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var out []string
	for _, s := range list {
		q := strings.TrimSpace(s.sql)
		up := strings.ToUpper(q)
		if strings.HasPrefix(up, "SET LOCAL") {
			if _, err = tx.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
			out = append(out, "-- "+q)
			continue
		}
		if !strings.HasPrefix(up, "SELECT") || strings.Contains(q, "pg_advisory") || strings.Contains(q, "app_sessions") {
			continue
		}
		rows, err := tx.Query(ctx, `EXPLAIN (ANALYZE, BUFFERS) `+q, s.args...)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			b.WriteString(line + "\n")
		}
		rows.Close()
		short := strings.Join(strings.Fields(q), " ")
		if len(short) > 100 {
			short = short[:100] + "…"
		}
		out = append(out, "`"+short+"`\n\n```text\n"+b.String()+"```")
	}
	return out
}

func TestDatabaseG33P0JIT(t *testing.T) {
	report := os.Getenv("BAOBEI_JIT_REPORT")
	if report == "" {
		t.Skip("NOT_RUN: set BAOBEI_JIT_REPORT to measure")
	}
	ctx := context.Background()
	total := 10000
	if v := os.Getenv("BAOBEI_SCALE_SIZES"); v != "" {
		total, _ = strconv.Atoi(v)
	}
	runs := 5
	if total < 5000 {
		runs = 15
	}
	label := os.Getenv("BAOBEI_JIT_LABEL")
	part := os.Getenv("BAOBEI_SCALE_PART")
	var out, plans strings.Builder
	f := g1FixtureFor(t)
	counts := scaleData(t, f, total)
	e := newPerfEnv(t, f, &out)
	if !statStatementsReady(t, e) {
		t.Fatal("server needs shared_preload_libraries=pg_stat_statements")
	}
	var version, jit, above, inline, optimize string
	_ = e.pool.QueryRow(ctx, `SELECT version(),current_setting('jit'),current_setting('jit_above_cost'),current_setting('jit_inline_above_cost'),current_setting('jit_optimize_above_cost')`).Scan(&version, &jit, &above, &inline, &optimize)
	fmt.Fprintf(&out, "\n## %s · %d 项目 · %s\n\n%s；会话 jit=%s，jit_above_cost=%s，jit_inline_above_cost=%s，jit_optimize_above_cost=%s。数据：合成 SQL 克隆（%v）。热重复 %d 次。\n\n",
		label, total, map[bool]string{true: "全部角色", false: part}[part == ""], version, jit, above, inline, optimize, counts, runs)
	fmt.Fprintf(&out, "| 场景 | 视角 | 查询数 | 冷首请求 | 热 P50 | 热 P95 | JIT 函数/请求 | JIT 耗时/请求 | 其中内联+优化+发射 | 执行（不含 JIT）/请求 | 规划/请求 | 服务端 CPU/请求 | 后端峰值内存 | 字节 |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	row := func(name, who string, fn func() int, plan bool) {
		coldS := "未测"
		if plan && restartPG(t) {
			e.pool.Reset()
			cold := e.measure(t, 1, fn)
			coldS = r10(cold.firstElapsed)
		}
		e.pool.Reset() // fresh backends: memory peak belongs to this scenario
		fn()           // warm-up (also prepares statements on the new connection)
		resetStatements(t, e)
		c0, _ := backendCPU()
		hot := e.measure(t, runs, fn)
		c1, hwm := backendCPU()
		st := readStatements(t, e)
		n := float64(runs)
		ms := func(v float64) string { return fmt.Sprintf("%.2fms", v/n) }
		line := fmt.Sprintf("| %s | %s | %d | %s | %s | %s | %.0f | %s | %s | %s | %s | %.0fms | %.1fMB | %d |\n", name, who, hot.queries, coldS, r10(hot.p50), r10(hot.p95),
			float64(st.functions)/n, ms(st.jitTotal()), ms(st.opt+st.emit+st.inline), ms(st.exec-st.jitTotal()), ms(st.plan), float64(c1-c0)*10/n, float64(hwm)/1024, hot.bytes)
		out.WriteString(line)
		if pf := os.Getenv("BAOBEI_SCALE_PROGRESS"); pf != "" {
			fh, _ := os.OpenFile(pf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fmt.Fprintf(fh, "%s %s %d %s", time.Now().Format("15:04:05"), label, total, line)
			fh.Close()
		}
		if plan {
			keys := make([]string, 0, len(st.statements))
			for k := range st.statements {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Fprintf(&plans, "\n### %s · %d · %s · %s\n\npg_stat_statements（%d 次热请求）：\n\n", label, total, name, who, runs)
			for _, k := range keys {
				fmt.Fprintf(&plans, "- %d × `%s`\n", st.statements[k], k)
			}
			for _, p := range replayPlans(t, e, hot.statements) {
				plans.WriteString("\n" + p + "\n")
			}
		}
	}
	// G3-4-A0 home: BAOBEI_SCALE_PART=home-<role> measures what the four-role
	// home page loads — workspace-summary and stage-summary — separately and
	// as one page load (the browser issues both concurrently; measured here
	// sequentially, i.e. an upper bound).
	if strings.HasPrefix(part, "home-") {
		who := strings.TrimPrefix(part, "home-")
		ws, ss := e.get(t, who, "/api/v1/workspace-summary"), e.get(t, who, "/api/v1/stage-summary")
		row("首页 workspace-summary", who, ws, true)
		row("首页 stage-summary", who, ss, true)
		row("首页整页（两请求合计）", who, func() int { return ws() + ss() }, false)
		appendReport(t, report, out.String()+plans.String())
		return
	}
	// G3-3A pipeline: BAOBEI_SCALE_PART=pipeline-<role> measures the stage
	// summary (unfiltered and with a base filter) and one stage column's first
	// and last page with the same harness.
	if strings.HasPrefix(part, "pipeline-") {
		who := strings.TrimPrefix(part, "pipeline-")
		row("阶段统计 stage-summary", who, e.get(t, who, "/api/v1/stage-summary"), true)
		row("阶段统计（province+status 筛选）", who, e.get(t, who, "/api/v1/stage-summary?province=%E5%90%88%E6%88%90%E7%9C%81&status=APPROVED"), false)
		row("阶段统计（q+updatedAfter 筛选）", who, e.get(t, who, "/api/v1/stage-summary?q=%E5%90%88%E6%88%90%E8%A7%84%E6%A8%A1%E9%A1%B9%E7%9B%AE1&updatedAfter=2000-01-01T00%3A00%3A00Z"), false)
		var sum StageSummary
		_, b := e.cl[who].call("GET", "/api/v1/stage-summary", nil)
		_ = json.Unmarshal(b, &sum)
		code, n := "", -1
		for _, sc := range sum.Stages {
			if sc.Count > n {
				code, n = sc.Code, sc.Count
			}
		}
		last := (n + 9) / 10
		if last < 1 {
			last = 1
		}
		row(fmt.Sprintf("阶段栏 %s 第1页（共 %d）", code, n), who, e.get(t, who, "/api/v1/projects?stage="+code+"&page=1&pageSize=10"), true)
		row(fmt.Sprintf("阶段栏 %s 末页(%d)", code, last), who, e.get(t, who, fmt.Sprintf("/api/v1/projects?stage=%s&page=%d&pageSize=10", code, last)), false)
		fmt.Fprintf(&out, "| 阶段分布 | %s | total=%d unclassified=%d amount=%+v | | | | | | | | | | | |\n", who, sum.Total, sum.Unclassified, sum.Amount)
		appendReport(t, report, out.String()+plans.String())
		return
	}
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
			last := (q.Counts[el] + 19) / 20
			if last < 1 {
				last = 1
			}
			row("审核队列 "+el+" 第1页", who, e.get(t, who, "/api/v1/reviews?kind=pending&eligibility="+el+"&pageSize=20&page=1"), el != "all" || who == "manager")
			row(fmt.Sprintf("审核队列 %s 末页(%d)", el, last), who, e.get(t, who, fmt.Sprintf("/api/v1/reviews?kind=pending&eligibility=%s&pageSize=20&page=%d", el, last)), false)
		}
		row("首页汇总（含最近项目、待跟进）", who, e.get(t, who, "/api/v1/workspace-summary"), true)
		fmt.Fprintf(&out, "| 计数 | %s | %v | | | | | | | | | | | |\n", who, q.Counts)
	}
	if part == "" || part == "sales" {
		row("首页汇总（含最近项目、待跟进）", "sales", e.get(t, "sales", "/api/v1/workspace-summary"), true)
	}
	appendReport(t, report, out.String()+plans.String())
	_ = pgx.ReadOnly
}
