package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// V1.1 R4 follow-up center. A read model over intake_projects.next_followup_at
// and next_action (kept current by POST /projects/{id}/activities); there is no
// second task table, so reading never creates business rows. Windows are
// disjoint and use the business time zone (China Standard Time, UTC+8, no
// daylight saving):
//
//	overdue      next_followup_at <  asOf
//	today        asOf <= next_followup_at < next business 00:00
//	week         next 00:00 <= next_followup_at < next 00:00 + 7 days
//	unscheduled  next_followup_at IS NULL
//
// Only open projects (not WON/LOST, not REJECTED/CANCELLED/CONVERTED, not in
// the recycle bin) appear. Scope is projectScope ∩ the scope selection (R2).

var businessZone = time.FixedZone("Asia/Shanghai", 8*3600)

type BusinessDay struct {
	Timezone string    `json:"timezone"`
	Today    time.Time `json:"todayStart"`
	Tomorrow time.Time `json:"tomorrowStart"`
	WeekEnd  time.Time `json:"weekEnd"`
}

func businessDay(asOf time.Time) BusinessDay {
	l := asOf.In(businessZone)
	start := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, businessZone)
	return BusinessDay{Timezone: "Asia/Shanghai", Today: start.UTC(), Tomorrow: start.AddDate(0, 0, 1).UTC(), WeekEnd: start.AddDate(0, 0, 8).UTC()}
}

var followupWindows = []string{"overdue", "today", "week", "unscheduled"}

type FollowupCenter struct {
	AsOf      time.Time      `json:"asOf"`
	Day       BusinessDay    `json:"businessDay"`
	Window    string         `json:"window"`
	Selection scopeSel       `json:"selection"`
	Counts    map[string]int `json:"counts"`
	Items     []Project      `json:"items"`
	Total     int            `json:"total"`
}

var followupParams = map[string]bool{"window": true, "scope": true, "memberId": true, "teamId": true, "q": true, "page": true, "pageSize": true}

func (a *App) followupCenter(ctx context.Context, u User, params map[string]string) (FollowupCenter, error) {
	out := FollowupCenter{Counts: map[string]int{}, Items: []Project{}}
	for k := range params {
		if !followupParams[k] {
			return out, listError("跟进中心不支持的筛选条件：" + k)
		}
	}
	window := params["window"]
	if window == "" {
		window = "today"
	}
	if !contains(followupWindows, window) {
		return out, listError("跟进时间窗口只支持 overdue、today、week、unscheduled")
	}
	if err := strictPaging(params); err != nil {
		return out, err
	}
	out.Window = window
	// The list itself is GET /projects with the same parameters: one source
	// of truth for membership, paging, hints and scope checks.
	list := map[string]string{"followup": window, "lifecycle": "open", "sort": "followup", "q": params["q"], "page": params["page"], "pageSize": params["pageSize"]}
	for _, k := range scopeKeys {
		if params[k] != "" {
			list[k] = params[k]
		}
	}
	for k, v := range list {
		if v == "" {
			delete(list, k)
		}
	}
	items, total, asOf, err := a.listAt(ctx, u, list)
	if err != nil {
		return out, err
	}
	out.Items, out.Total, out.AsOf, out.Day = items, total, asOf, businessDay(asOf)
	// Counts of all four windows: one statement at the same asOf.
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	base := map[string]string{"lifecycle": "open", "q": params["q"]}
	for _, k := range scopeKeys {
		base[k] = params[k]
	}
	sel, err := resolveScope(ctx, tx, u, base)
	if err != nil {
		return out, err
	}
	out.Selection = sel
	if out.Selection.Scope == "" {
		out.Selection.Scope = availableScopes(u.Role)[0]
	}
	if base["q"] == "" {
		delete(base, "q")
	}
	where, args, err := projectFilters(u, base, asOf)
	if err != nil {
		return out, err
	}
	where, args = sel.narrow(where, args)
	d := out.Day
	args = append(args, asOf, d.Tomorrow, d.WeekEnd)
	n := len(args)
	p := func(i int) string { return "$" + strconv.Itoa(i) }
	var overdue, today, week, unscheduled int
	err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE p.next_followup_at<`+p(n-2)+`),
 count(*) FILTER (WHERE p.next_followup_at>=`+p(n-2)+` AND p.next_followup_at<`+p(n-1)+`),
 count(*) FILTER (WHERE p.next_followup_at>=`+p(n-1)+` AND p.next_followup_at<`+p(n)+`),
 count(*) FILTER (WHERE p.next_followup_at IS NULL)
 FROM intake_projects p`+where, args...).Scan(&overdue, &today, &week, &unscheduled)
	if err != nil {
		return out, err
	}
	out.Counts = map[string]int{"overdue": overdue, "today": today, "week": week, "unscheduled": unscheduled}
	return out, tx.Commit(ctx)
}

func (a *App) followupHandler(w http.ResponseWriter, r *http.Request, u User) error {
	params := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			fail(w, 400, "INVALID_FILTER", "筛选条件重复："+k)
			return nil
		}
		params[k] = v[0]
	}
	out, err := a.followupCenter(r.Context(), u, params)
	if le, ok := err.(listError); ok {
		fail(w, 400, "INVALID_FILTER", le.Error())
		return nil
	}
	if err != nil {
		return err
	}
	respond(w, 200, out)
	return nil
}
