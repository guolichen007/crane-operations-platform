// Command concurrency drives internal/operations.Summary (the production repository code) with N
// concurrent callers against a database pool sized like cloud-api (SetMaxOpenConns(10)), and reports
// the latency distribution, errors and PostgreSQL backend usage. It also checks pool exhaustion
// (callers queue on the pool; a caller whose context expires while queued gets ErrUnavailable, not
// partial data) and request cancellation. SYNTHETIC capacity evidence only.
//
// Usage: go run ./tools/perf/operations-summary/concurrency -dsn postgres://... -user u-p200 -n 10,30 -requests 120
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/geo"
	"github.com/guolichen007/crane-operations-platform/internal/operations"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("BENCH_DSN"), "PostgreSQL DSN of the scratch benchmark database")
	user := flag.String("user", "u-p200", "user id from the synthetic dataset")
	levels := flag.String("n", "10,30", "concurrency levels")
	requests := flag.Int("requests", 120, "requests per level")
	pool := flag.Int("pool", 10, "max open connections (cloud-api uses 10)")
	flag.Parse()
	db, err := sql.Open("pgx", *dsn)
	must(err)
	db.SetMaxOpenConns(*pool)
	ix, err := geo.Default()
	must(err)
	cfg := operations.Config{StaleAfter: operations.DefaultStaleAfter, MaxFutureSkew: operations.DefaultMaxFutureSkew}
	ref := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	now := func() time.Time { return ref }
	call := func(ctx context.Context) (time.Duration, error) {
		start := time.Now()
		_, err := operations.Summary(ctx, db, ix, cfg, operations.Request{User: *user, Window: "PT24H"}, now)
		return time.Since(start), err
	}
	_, _ = call(context.Background()) // warm the pool and plan cache
	var seq []time.Duration
	for i := 0; i < 15; i++ {
		d, err := call(context.Background())
		must(err)
		seq = append(seq, d)
	}
	sort.Slice(seq, func(i, j int) bool { return seq[i] < seq[j] })
	fmt.Printf("sequential (1 caller, end-to-end Go repository incl. region map + contract validation): p50=%v min=%v max=%v\n", seq[7].Round(time.Millisecond), seq[0].Round(time.Millisecond), seq[14].Round(time.Millisecond))
	fmt.Printf("# operations summary concurrency — user=%s pool=%d requests/level=%d (synthetic)\n", *user, *pool, *requests)
	for _, s := range strings.Split(*levels, ",") {
		n, _ := strconv.Atoi(s)
		var mu sync.Mutex
		var lat []time.Duration
		errs := 0
		jobs := make(chan struct{}, *requests)
		for i := 0; i < *requests; i++ {
			jobs <- struct{}{}
		}
		close(jobs)
		started := time.Now()
		var wg sync.WaitGroup
		for w := 0; w < n; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range jobs {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // handler timeout
					d, err := call(ctx)
					cancel()
					mu.Lock()
					if err != nil {
						errs++
					} else {
						lat = append(lat, d)
					}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		wall := time.Since(started)
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		pct := func(p float64) time.Duration {
			if len(lat) == 0 {
				return 0
			}
			return lat[int(p*float64(len(lat)-1))]
		}
		st := db.Stats() // cumulative pool counters since start
		fmt.Printf("concurrency=%d ok=%d errors=%d p50=%v p95=%v p99=%v max=%v throughput=%.1f/s poolWaitCount=%d poolWait=%v\n",
			n, len(lat), errs, pct(.5).Round(time.Millisecond), pct(.95).Round(time.Millisecond), pct(.99).Round(time.Millisecond), pct(1).Round(time.Millisecond),
			float64(len(lat))/wall.Seconds(), st.WaitCount, st.WaitDuration.Round(time.Millisecond))
	}
	// Pool exhaustion: hold every pooled connection, then a summary with a 300 ms deadline must fail
	// cleanly with ErrUnavailable (it cannot obtain a connection) — never return numbers.
	held := make([]*sql.Conn, 0, *pool)
	for i := 0; i < *pool; i++ {
		c, err := db.Conn(context.Background())
		must(err)
		held = append(held, c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	d, err := call(ctx)
	cancel()
	fmt.Printf("pool exhausted: err=%v unavailable=%v after=%v\n", err, errors.Is(err, operations.ErrUnavailable), d.Round(time.Millisecond))
	for _, c := range held {
		_ = c.Close()
	}
	// Cancellation mid-query: cancel after 5 ms; the backend query must be cancelled (no lingering active statement).
	ctx, cancel = context.WithCancel(context.Background())
	go func() { time.Sleep(5 * time.Millisecond); cancel() }()
	_, err = call(ctx)
	time.Sleep(200 * time.Millisecond)
	var lingering int
	_ = db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND state = 'active' AND query LIKE '%agg_alarm%' AND pid <> pg_backend_pid()`).Scan(&lingering)
	fmt.Printf("cancel mid-query: err=%v unavailable=%v lingeringActiveStatements=%d\n", err, errors.Is(err, operations.ErrUnavailable), lingering)
	// After all of the above the pool still serves correct summaries.
	_, err = call(context.Background())
	fmt.Printf("recovery: err=%v\n", err)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
