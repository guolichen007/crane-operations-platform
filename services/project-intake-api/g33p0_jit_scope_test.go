package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// G3-3-P0: JIT is switched off only inside the review queue and workspace
// summary read transactions (SET LOCAL jit = off). These tests observe what
// the server really did: every connection loads auto_explain, which sends the
// executed plan of each statement to the client as a NOTICE (with ANALYZE, so
// a "JIT" section appears whenever the statement was JIT compiled), and sets
// jit_above_cost=0 so any statement that is still JIT-eligible compiles. The
// server-wide/session default stays jit=on.

type explained struct {
	query string
	jit   bool
	pid   uint32
}

type jitProbe struct {
	mu   sync.Mutex
	list []explained
}

func (p *jitProbe) take() []explained {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.list
	p.list = nil
	return out
}

func newJITPool(t *testing.T, db *pgxpool.Pool, maxConns int32, p *jitProbe) *pgxpool.Pool {
	t.Helper()
	cfg := db.Config().Copy()
	cfg.MaxConns = maxConns
	cfg.ConnConfig.OnNotice = func(c *pgconn.PgConn, n *pgconn.Notice) {
		i := strings.Index(n.Message, "plan:")
		if i < 0 {
			return
		}
		var plan struct {
			Query string         `json:"Query Text"`
			JIT   map[string]any `json:"JIT"`
		}
		if json.Unmarshal([]byte(n.Message[i+5:]), &plan) != nil {
			return
		}
		p.mu.Lock()
		p.list = append(p.list, explained{strings.Join(strings.Fields(plan.Query), " "), plan.JIT != nil, c.PID()})
		p.mu.Unlock()
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, `LOAD 'auto_explain'; SET auto_explain.log_min_duration=0; SET auto_explain.log_analyze=on;
 SET auto_explain.log_format=json; SET auto_explain.log_level=notice; SET jit_above_cost=0`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// The statements that run inside the protected transactions.
func isTargetSQL(q string) bool {
	return strings.HasPrefix(q, "SELECT now(),count(*),count(*) FILTER") || // queue counts
		strings.Contains(q, "AS submitted_at FROM intake_projects p") || // queue page
		strings.HasPrefix(q, "SELECT count(*), count(*) FILTER (WHERE p.intake_status='DRAFT')") || // summary counts
		strings.HasPrefix(q, "SELECT p.id::text,p.registration_no,p.project_name,coalesce(p.document->>'customer','')") // summary recent/followups
}

func jitUsers(t *testing.T, db *pgxpool.Pool) []User {
	t.Helper()
	us := g32cUsers(t, db)
	return []User{us["manager"], us["director"], us["admin"], us["sales"], us["other-manager"]}
}

func showJIT(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) string {
	t.Helper()
	var v string
	if err := q.QueryRow(context.Background(), `SHOW jit`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func g33Data(t *testing.T) g1Fixture {
	t.Helper()
	f := g1FixtureFor(t)
	// A real review state (API) plus a synthetic population so every scope
	// and eligibility has rows.
	c := login(t, f.server, "synthetic-c")
	g1Submit(t, f.sales, g1Create(t, f.sales, "合成JIT范围"))
	g1Submit(t, c, g1Create(t, c, "合成JIT范围"))
	g1Submit(t, f.other, g1Create(t, f.other, "合成JIT范围"))
	scaleData(t, f, 300)
	var jitAvailable bool
	_ = f.db.QueryRow(context.Background(), `SELECT pg_jit_available()`).Scan(&jitAvailable)
	if !jitAvailable {
		t.Skip("NOT_RUN: server has no JIT provider")
	}
	return f
}

func TestDatabaseG33P0JITScope(t *testing.T) {
	ctx := context.Background()
	f := g33Data(t)
	users := jitUsers(t, f.db)

	t.Run("targets-run-without-jit-and-default-is-restored", func(t *testing.T) {
		probe := &jitProbe{}
		pool := newJITPool(t, f.db, 1, probe) // one connection: every call reuses it
		app := &App{db: pool, cfg: f.app.cfg}
		if v := showJIT(t, pool); v != "on" {
			t.Fatal("session default must stay on", v)
		}
		// Harness check: an ordinary statement on this connection is JIT compiled.
		probe.take()
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM intake_projects WHERE intake_status<>'X'`).Scan(&n)
		if got := probe.take(); len(got) != 1 || !got[0].jit {
			t.Fatalf("harness cannot observe JIT: %+v", got)
		}
		// 8 rounds: beyond the 5 custom plans after which a prepared statement
		// may switch to a cached generic plan.
		seen := map[string]int{}
		for round := 0; round < 8; round++ {
			for _, u := range users {
				if _, err := app.workspaceSummary(ctx, u); err != nil {
					t.Fatal(err)
				}
				if (ProjectPolicy{u}).Manage() {
					for _, el := range []string{"reviewable", "blocked", "all"} {
						if _, err := app.reviewQueue(ctx, u, el, 1, 20); err != nil {
							t.Fatal(err)
						}
					}
				}
				if v := showJIT(t, pool); v != "on" {
					t.Fatalf("jit=%s leaked onto the pooled connection after %s", v, u.Role)
				}
			}
			for _, e := range probe.take() {
				if isTargetSQL(e.query) {
					seen[e.query[:40]]++
					if e.jit {
						t.Fatalf("round %d: protected statement was JIT compiled: %.120s", round, e.query)
					}
				}
			}
		}
		if len(seen) != 4 {
			t.Fatalf("expected the 4 protected statement kinds, saw %v", seen)
		}
		// Statements shared with other endpoints (review facts, owner teams)
		// ran inside the protected transactions above; outside they must
		// still be JIT-eligible, also once a generic plan exists.
		var ids []string
		rows, _ := pool.Query(ctx, `SELECT id::text FROM intake_projects WHERE intake_status IN ('SUBMITTED','UNDER_REVIEW') ORDER BY id LIMIT 20`)
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		probe.take()
		for i := 0; i < 8; i++ {
			if _, err := loadReviewFacts(ctx, pool, users[1], ids); err != nil {
				t.Fatal(err)
			}
		}
		for _, e := range probe.take() {
			if strings.Contains(e.query, "p.id=ANY($4::uuid[])") && !e.jit {
				t.Fatalf("shared review-facts statement lost JIT outside the protected transaction (plan cache leak)")
			}
		}
	})

	t.Run("concurrent-requests-do-not-change-other-queries", func(t *testing.T) {
		probe := &jitProbe{}
		pool := newJITPool(t, f.db, 4, probe)
		app := &App{db: pool, cfg: f.app.cfg}
		var wg sync.WaitGroup
		errs := make(chan error, 64)
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				u := users[w%len(users)]
				for i := 0; i < 6; i++ {
					var err error
					switch (w + i) % 3 {
					case 0:
						_, err = app.workspaceSummary(ctx, u)
					case 1:
						if (ProjectPolicy{u}).Manage() {
							_, err = app.reviewQueue(ctx, u, []string{"reviewable", "blocked", "all"}[i%3], 1+i%2, 10)
						}
					default:
						var n int
						err = pool.QueryRow(ctx, `SELECT count(*) FROM intake_projects WHERE intake_status<>'X'`).Scan(&n)
					}
					if err != nil {
						errs <- err
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		other, protected := 0, 0
		for _, e := range probe.take() {
			switch {
			case isTargetSQL(e.query):
				protected++
				if e.jit {
					t.Fatalf("protected statement JIT compiled under concurrency: %.100s", e.query)
				}
			case strings.Contains(e.query, "intake_status<>'X'"):
				other++
				if !e.jit {
					t.Fatal("an unrelated concurrent query lost JIT")
				}
			}
		}
		if other == 0 || protected == 0 {
			t.Fatal("nothing observed", other, protected)
		}
		// Every pooled connection is back to the default.
		for i := 0; i < 4; i++ {
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if v := showJIT(t, conn); v != "on" {
				t.Fatal("pooled connection kept jit", v)
			}
			defer conn.Release()
		}
	})

	t.Run("cancel-timeout-terminate-and-no-open-transaction", func(t *testing.T) {
		probe := &jitProbe{}
		pool := newJITPool(t, f.db, 1, probe)
		app := &App{db: pool, cfg: f.app.cfg}
		director := users[1]
		// Cancelled request context: no result, connection reusable with jit on.
		for _, fn := range []func(context.Context) error{
			func(c context.Context) error { _, err := app.workspaceSummary(c, director); return err },
			func(c context.Context) error { _, err := app.reviewQueue(c, director, "blocked", 1, 20); return err },
		} {
			c, cancel := context.WithCancel(ctx)
			cancel()
			if err := fn(c); err == nil {
				t.Fatal("cancelled call succeeded")
			}
			c, cancel = context.WithTimeout(ctx, 2*time.Millisecond)
			_ = fn(c) // may or may not finish in time; must never leak
			cancel()
			if v := showJIT(t, pool); v != "on" {
				t.Fatal("jit leaked after cancellation", v)
			}
		}
		// SQL timeout inside the transaction: error, rollback, default restored.
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Exec(ctx, `SET statement_timeout='1ms'`); err != nil {
			t.Fatal(err)
		}
		pid := conn.Conn().PgConn().PID()
		conn.Release()
		_, errSummary := app.workspaceSummary(ctx, director)
		_, errQueue := app.reviewQueue(ctx, director, "reviewable", 1, 20)
		var pgErr *pgconn.PgError
		if !errors.As(errSummary, &pgErr) || !errors.As(errQueue, &pgErr) {
			t.Fatal("statement timeout expected", errSummary, errQueue)
		}
		conn, _ = pool.Acquire(ctx)
		if conn.Conn().PgConn().PID() != pid {
			t.Fatal("expected the same pooled connection")
		}
		if v := showJIT(t, conn); v != "on" {
			t.Fatal("jit leaked after statement timeout", v)
		}
		_, _ = conn.Exec(ctx, `RESET statement_timeout`)
		conn.Release()
		// Connection killed by the server: the call fails, the pool replaces
		// the connection, the next call succeeds with the default setting.
		if _, err = f.db.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		_, _ = app.reviewQueue(ctx, director, "all", 1, 20)
		if _, err = app.workspaceSummary(ctx, director); err != nil {
			t.Fatal("pool did not recover", err)
		}
		if v := showJIT(t, pool); v != "on" {
			t.Fatal(v)
		}
		// No idle-in-transaction session is left behind.
		var open int
		_ = f.db.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND state LIKE 'idle in transaction%'`).Scan(&open)
		if open != 0 {
			t.Fatal("open transaction left", open)
		}
	})
}

// legacyWorkspaceSummary and legacyReviewQueue are verbatim copies of the
// pre-G3-3-P0 functions (3e714c7) used as the equivalence oracle.
func legacyWorkspaceSummary(ctx context.Context, a *App, u User) (WorkspaceSummary, error) {
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	s := WorkspaceSummary{AsOf: asOf, Scope: map[string]string{salesRole: "OWN", managerRole: "TEAM", directorRole: "ALL_ASSIGNED", adminRole: "ALL"}[u.Role], Counts: map[string]int{}}
	var total, drafts, returned, unassigned, submitted, review, approved, overdue, upcoming, queue, reviewable int
	err := a.db.QueryRow(ctx, `SELECT count(*),
 count(*) FILTER (WHERE p.intake_status='DRAFT'),
 count(*) FILTER (WHERE p.intake_status='RETURNED'),
 count(*) FILTER (WHERE p.intake_status='DRAFT' AND p.owner_subject IS NULL),
 count(*) FILTER (WHERE p.intake_status='SUBMITTED'),
 count(*) FILTER (WHERE p.intake_status='UNDER_REVIEW'),
 count(*) FILTER (WHERE p.intake_status='APPROVED'),
 count(*) FILTER (WHERE p.next_followup_at<$4),
 count(*) FILTER (WHERE p.next_followup_at>=$4 AND p.next_followup_at<$5),
 count(*) FILTER (WHERE $2<>'baobei_sales' AND p.intake_status IN ('SUBMITTED','UNDER_REVIEW')),
 count(*) FILTER (WHERE `+reviewableSQL("p")+`)
 FROM intake_projects p WHERE `+projectScope("p"), u.Subject, u.Role, u.TeamID, asOf, asOf.Add(7*24*time.Hour)).
		Scan(&total, &drafts, &returned, &unassigned, &submitted, &review, &approved, &overdue, &upcoming, &queue, &reviewable)
	if err != nil {
		return s, err
	}
	for k, v := range map[string]int{"total": total, "drafts": drafts, "returned": returned, "unassignedDrafts": unassigned, "submitted": submitted, "underReview": review, "approved": approved, "overdue": overdue, "upcoming7d": upcoming, "reviewQueue": queue, "reviewable": reviewable} {
		s.Counts[k] = v
	}
	recent := func(where, order string, limit int, args ...any) ([]RecentItem, error) {
		rows, err := a.db.Query(ctx, `SELECT p.id::text,p.registration_no,p.project_name,coalesce(p.document->>'customer',''),p.intake_status,p.sales_stage,`+displayName("p.owner_subject")+`,p.updated_at,p.next_followup_at,p.next_action
 FROM intake_projects p WHERE `+projectScope("p")+where+` ORDER BY `+order+`,p.id LIMIT `+map[int]string{5: "5", 6: "6"}[limit], append([]any{u.Subject, u.Role, u.TeamID}, args...)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []RecentItem{}
		for rows.Next() {
			var r RecentItem
			if err = rows.Scan(&r.ID, &r.RegistrationNo, &r.ProjectName, &r.Customer, &r.IntakeStatus, &r.SalesStage, &r.OwnerName, &r.UpdatedAt, &r.NextFollowupAt, &r.NextAction); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	if s.Recent, err = recent(``, `p.updated_at DESC`, 6); err != nil {
		return s, err
	}
	s.Followup, err = recent(` AND p.next_followup_at<$4`, `p.next_followup_at ASC`, 5, asOf.Add(7*24*time.Hour))
	return s, err
}

func legacyReviewQueue(ctx context.Context, a *App, u User, eligibility string, page, size int) (ReviewQueue, error) {
	out := ReviewQueue{Items: []Project{}, Counts: map[string]int{}}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	base := ` FROM intake_projects p WHERE p.intake_status IN ('SUBMITTED','UNDER_REVIEW') AND $2 IN ('baobei_manager','baobei_director','baobei_admin') AND ` + projectScope("p")
	var reviewable, all int
	if err = tx.QueryRow(ctx, `SELECT now(),count(*),count(*) FILTER (WHERE `+reviewableSQL("p")+`)`+base, u.Subject, u.Role, u.TeamID).Scan(&out.AsOf, &all, &reviewable); err != nil {
		return out, err
	}
	out.AsOf = out.AsOf.UTC()
	out.Counts = map[string]int{"reviewable": reviewable, "blocked": all - reviewable, "all": all}
	out.Total = out.Counts[eligibility]
	rows, err := tx.Query(ctx, `SELECT `+columns+`,`+submittedAtSQL+` AS submitted_at`+base+` `+queueEligibility[eligibility]+`
 ORDER BY submitted_at ASC NULLS LAST,p.id LIMIT $4 OFFSET $5`, u.Subject, u.Role, u.TeamID, size, (page-1)*size)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p Project
		var submitted *time.Time
		if p, err = scan(scanTail{rows, &submitted}); err != nil {
			rows.Close()
			return out, err
		}
		p.SubmittedAt = submitted
		out.Items = append(out.Items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	ptrs := make([]*Project, len(out.Items))
	for i := range out.Items {
		ptrs[i] = &out.Items[i]
	}
	if err = a.batchHints(ctx, tx, u, ptrs); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

var g33Transcript strings.Builder

func TestDatabaseG33P0Equivalence(t *testing.T) {
	ctx := context.Background()
	f := g33Data(t)
	app := f.app
	users := jitUsers(t, f.db)
	norm := func(v any) string { // asOf differs per call by definition; everything else must match
		b := mustJSON(v)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		delete(m, "asOf")
		return string(mustJSON(m))
	}
	cases := 0
	for _, u := range users {
		before := time.Now().UTC()
		want, err := legacyWorkspaceSummary(ctx, app, u)
		if err != nil {
			t.Fatal(err)
		}
		got, err := app.workspaceSummary(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		if a, b := norm(want), norm(got); a != b {
			t.Fatalf("%s summary differs\nwant %s\ngot  %s", u.Subject, a, b)
		}
		// asOf keeps its meaning: the API process clock at request start, UTC, microseconds.
		if got.AsOf.Before(before) || got.AsOf.After(time.Now().UTC()) || got.AsOf.Location() != time.UTC || got.AsOf.Nanosecond()%1000 != 0 {
			t.Fatal("summary asOf semantics changed", got.AsOf)
		}
		if len(got.Recent) == 0 {
			t.Fatal("fixture: empty recent for", u.Subject)
		}
		fmt.Fprintf(&g33Transcript, "summary %s counts=%v recent=%d followups=%d\n", u.Subject, got.Counts, len(got.Recent), len(got.Followup))
		cases++
		if !(ProjectPolicy{u}).Manage() {
			continue
		}
		for _, el := range []string{"reviewable", "blocked", "all"} {
			for _, size := range []int{7, 20, 100} {
				first, err := legacyReviewQueue(ctx, app, u, el, 1, size)
				if err != nil {
					t.Fatal(err)
				}
				last := (first.Total + size - 1) / size
				for _, page := range []int{1, 2, last, last + 1} {
					if page < 1 {
						continue
					}
					want, err := legacyReviewQueue(ctx, app, u, el, page, size)
					if err != nil {
						t.Fatal(err)
					}
					var dbBefore time.Time
					_ = f.db.QueryRow(ctx, `SELECT now()`).Scan(&dbBefore)
					got, err := app.reviewQueue(ctx, u, el, page, size)
					if err != nil {
						t.Fatal(err)
					}
					if a, b := norm(want), norm(got); a != b {
						t.Fatalf("%s %s page %d/%d differs\nwant %s\ngot  %s", u.Subject, el, page, size, a, b)
					}
					// asOf remains the database transaction time of the read snapshot.
					if got.AsOf.Before(dbBefore) || got.AsOf.Location() != time.UTC {
						t.Fatal("queue asOf semantics changed", got.AsOf, dbBefore)
					}
					cases++
				}
				fmt.Fprintf(&g33Transcript, "queue %s %s size=%d total=%d counts=%v pages=%d\n", u.Subject, el, size, first.Total, first.Counts, last)
			}
		}
	}
	keys := strings.Split(strings.TrimSpace(g33Transcript.String()), "\n")
	sort.Strings(keys)
	t.Logf("G33P0_EQUIVALENCE_CASES=%d", cases)
	if path := os.Getenv("BAOBEI_G33_TRANSCRIPT"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(keys, "\n")+"\n"), 0o600)
	}
}
