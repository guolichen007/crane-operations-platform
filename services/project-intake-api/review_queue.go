package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// G3-2B review queue. One read-only REPEATABLE READ transaction gives the page,
// the filtered total, the three counts and the per-item hints the same
// snapshot; asOf is that transaction's start time (now()). Membership uses the
// same reviewableSQL/projectScope as the home summary, so for an unchanged data
// state the home tile and the list agree. Final authority stays with the POST
// transaction (version, independence, frozen evidence, audit).

type ReviewQueue struct {
	Items  []Project      `json:"items"`
	Total  int            `json:"total"`
	AsOf   time.Time      `json:"asOf"`
	Counts map[string]int `json:"counts"`
}

var queueEligibility = map[string]string{
	"reviewable": `AND ` + reviewableSQL("p"),
	"blocked":    `AND NOT ` + reviewableSQL("p"),
	"all":        ``,
}

// submittedAtSQL is the trusted submission time: the frozen submission row of
// the current round. Migrated rows without one have no waiting time.
const submittedAtSQL = `(SELECT s.created_at FROM project_submissions s WHERE s.project_id=p.id AND s.revision=p.submission_revision)`

func (a *App) reviewQueue(ctx context.Context, u User, eligibility string, page, size int) (ReviewQueue, error) {
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

// scanTail lets scan() read the standard columns plus trailing extras.
type scanTail struct {
	row   pgx.Row
	extra *(*time.Time)
}

func (s scanTail) Scan(dest ...any) error { return s.row.Scan(append(dest, s.extra)...) }

// reviewQueueHandler serves GET /api/v1/reviews when eligibility is present.
// Without eligibility the pre-G3-2B list is unchanged.
func (a *App) reviewQueueHandler(w http.ResponseWriter, r *http.Request, u User) error {
	q := r.URL.Query()
	eligibility := q.Get("eligibility")
	if _, ok := queueEligibility[eligibility]; !ok {
		fail(w, 400, "INVALID_FILTER", "审核资格筛选只支持 reviewable、blocked、all")
		return nil
	}
	if kind := q.Get("kind"); kind != "" && kind != "pending" {
		fail(w, 400, "INVALID_FILTER", "审核资格筛选只适用于待审核队列")
		return nil
	}
	num := func(key string, def, max int) (int, bool) {
		v := q.Get(key)
		if v == "" {
			return def, true
		}
		n, err := strconv.Atoi(v)
		return n, err == nil && n >= 1 && n <= max
	}
	page, ok := num("page", 1, 1_000_000)
	if !ok {
		fail(w, 400, "INVALID_FILTER", "页码不正确")
		return nil
	}
	size, ok := num("pageSize", 20, 100)
	if !ok {
		fail(w, 400, "INVALID_FILTER", "每页数量需在 1 到 100 之间")
		return nil
	}
	out, err := a.reviewQueue(r.Context(), u, eligibility, page, size)
	if err != nil {
		return err
	}
	respond(w, 200, out)
	return nil
}
