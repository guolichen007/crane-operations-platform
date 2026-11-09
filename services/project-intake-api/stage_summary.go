package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// G3-3A: real sales-stage pipeline. sales_stage (business stage) and
// intake_status (filing review state) are independent dimensions; the stage
// summary counts every project in the caller's projectScope, whatever its
// intake status, exactly like the default /projects list.

// projectFilters builds the scope predicate plus the whitelisted filters
// shared by GET /projects and GET /stage-summary. The first three arguments
// are always subject, role and team (projectScope).
func projectFilters(u User, params map[string]string, asOf time.Time) (string, []any, error) {
	args := []any{u.Subject, u.Role, u.TeamID}
	where := ` WHERE ` + projectScope("p")
	add := func(sql string, v any) {
		args = append(args, v)
		where += strings.ReplaceAll(sql, "?", "$"+strconv.Itoa(len(args)))
	}
	if v := params["q"]; v != "" {
		add(` AND (p.project_name ILIKE '%'||?||'%' OR p.registration_no ILIKE '%'||?||'%' OR p.document->>'customer' ILIKE '%'||?||'%')`, v)
	}
	if v := params["status"]; v != "" && !contains(strings.Fields("DRAFT SUBMITTED UNDER_REVIEW APPROVED RETURNED REJECTED EXPIRED CANCELLED CONVERTED"), v) {
		return "", nil, listError("报备状态不正确")
	}
	if v := params["stage"]; v != "" && !contains(stages, v) {
		return "", nil, listError("销售阶段不正确")
	}
	for k, col := range map[string]string{"status": "p.intake_status", "stage": "p.sales_stage", "province": "p.document->'location'->>'province'"} {
		if v := params[k]; v != "" {
			add(` AND `+col+`=?`, v)
		}
	}
	if v := params["updatedAfter"]; v != "" {
		if _, e := time.Parse(time.RFC3339, v); e != nil {
			return "", nil, listError("更新时间格式不正确")
		}
		add(` AND p.updated_at>=?::timestamptz`, v)
	}
	// Follow-up windows share one asOf: overdue=[-inf,asOf), upcoming=[asOf,asOf+7d).
	switch params["followup"] {
	case "":
	case "overdue":
		add(` AND p.next_followup_at<?::timestamptz`, asOf)
	case "upcoming":
		add(` AND p.next_followup_at>=?::timestamptz`, asOf)
		add(` AND p.next_followup_at<?::timestamptz`, asOf.Add(7*24*time.Hour))
	default:
		return "", nil, listError("跟进时间筛选不正确")
	}
	switch params["assignment"] {
	case "":
	case "unassigned":
		where += ` AND p.owner_subject IS NULL`
	case "assigned":
		where += ` AND p.owner_subject IS NOT NULL`
	default:
		return "", nil, listError("归属筛选不正确")
	}
	if params["due"] == "true" {
		add(` AND p.next_followup_at<?::timestamptz`, asOf.Add(7*24*time.Hour))
	}
	if params["review"] == "pending" {
		where += ` AND p.intake_status IN ('SUBMITTED','UNDER_REVIEW')`
	}
	if params["review"] == "protection" {
		where += ` AND p.protection_status IN ('WARNING','EXPIRING','EXPIRED') AND p.intake_status IN ('SUBMITTED','UNDER_REVIEW','APPROVED')`
	}
	return where, args, nil
}

// stageSummaryParams are the base filters of the pipeline. They apply to all
// stages; a stage choice never narrows the other columns, so stage, paging,
// sorting and window filters are rejected here.
var stageSummaryParams = map[string]bool{"q": true, "status": true, "province": true, "updatedAfter": true}

type StageCount struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}
type StageSummary struct {
	AsOf    time.Time         `json:"asOf"`
	Scope   string            `json:"scope"`
	Filters map[string]string `json:"filters"`
	Total   int               `json:"total"`
	Stages  []StageCount      `json:"stages"`
	// Projects in scope whose stored sales_stage is not one of the 12 codes.
	// Always reported (normally 0) so a data-quality issue is never hidden:
	// sum(stages) + unclassified == total.
	Unclassified int `json:"unclassified"`
}

var scopeLabels = map[string]string{salesRole: "OWN", managerRole: "TEAM", directorRole: "ALL_ASSIGNED", adminRole: "ALL"}

func (a *App) stageSummary(ctx context.Context, u User, params map[string]string) (StageSummary, error) {
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	out := StageSummary{AsOf: asOf, Scope: scopeLabels[u.Role], Filters: map[string]string{}, Stages: make([]StageCount, len(stages))}
	for k, v := range params {
		if !stageSummaryParams[k] {
			return out, listError("阶段统计不支持的筛选条件：" + k)
		}
		if v != "" {
			out.Filters[k] = v
		}
	}
	where, args, err := projectFilters(u, params, asOf)
	if err != nil {
		return out, err
	}
	// One set aggregation over the scoped, filtered projects.
	rows, err := a.db.Query(ctx, `SELECT p.sales_stage,count(*) FROM intake_projects p`+where+` GROUP BY p.sales_stage`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var stage string
		var n int
		if err = rows.Scan(&stage, &n); err != nil {
			return out, err
		}
		counts[stage] += n
		out.Total += n
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	known := 0
	for i, code := range stages {
		out.Stages[i] = StageCount{Code: code, Count: counts[code]}
		known += counts[code]
	}
	out.Unclassified = out.Total - known
	return out, nil
}

func (a *App) stageSummaryHandler(w http.ResponseWriter, r *http.Request, u User) error {
	params := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			fail(w, 400, "INVALID_FILTER", "筛选条件重复："+k)
			return nil
		}
		params[k] = v[0]
	}
	out, err := a.stageSummary(r.Context(), u, params)
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

// strictPaging rejects malformed page/pageSize on GET /projects instead of
// silently falling back to defaults (G3-3A: pipeline columns page on it).
func strictPaging(params map[string]string) error {
	for key, max := range map[string]int{"page": 1_000_000, "pageSize": 100} {
		v, ok := params[key]
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > max {
			return listError(map[string]string{"page": "页码不正确", "pageSize": "每页数量需在 1 到 100 之间"}[key])
		}
	}
	return nil
}
