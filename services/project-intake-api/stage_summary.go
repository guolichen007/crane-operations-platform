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
	Code   string        `json:"code"`
	Count  int           `json:"count"`
	Amount AmountSummary `json:"amount"`
}

// AmountSummary (G3-3B) covers the estimated project amount only — not a
// contract, revenue or forecast. KnownTotal is a fixed-point decimal string
// computed by PostgreSQL numeric SUM; it includes only KNOWN amounts. For
// every group: knownCount + unknownCount + legacyUnconfirmedCount +
// invalidCount = project count. knownImplicitCount is the part of knownCount
// that comes from migrated records without an explicit knowledge flag (G1
// rule: an unflagged positive amount is KNOWN), disclosed for transparency.
type AmountSummary struct {
	KnownTotal             string `json:"knownTotal"`
	KnownCount             int    `json:"knownCount"`
	KnownZeroCount         int    `json:"knownZeroCount"`
	KnownImplicitCount     int    `json:"knownImplicitCount"`
	UnknownCount           int    `json:"unknownCount"`
	LegacyUnconfirmedCount int    `json:"legacyUnconfirmedCount"`
	InvalidCount           int    `json:"invalidCount"`
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
	// Amount over all projects (= sum of stages[].amount + unclassifiedAmount).
	Amount             AmountSummary `json:"amount"`
	UnclassifiedAmount AmountSummary `json:"unclassifiedAmount"`
}

var scopeLabels = map[string]string{salesRole: "OWN", managerRole: "TEAM", directorRole: "ALL_ASSIGNED", adminRole: "ALL"}

// stageCodesSQL lists the 12 codes; any other stored value groups as unclassified.
var stageCodesSQL = "'" + strings.Join(stages, "','") + "'"

func zeroAmount() AmountSummary { return AmountSummary{KnownTotal: "0.00"} }

func (a *App) stageSummary(ctx context.Context, u User, params map[string]string) (StageSummary, error) {
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	out := StageSummary{AsOf: asOf, Scope: scopeLabels[u.Role], Filters: map[string]string{}, Stages: make([]StageCount, len(stages)), Amount: zeroAmount(), UnclassifiedAmount: zeroAmount()}
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
	// One statement (one snapshot): per stage group plus the grand total via
	// GROUPING SETS. Amount classification is amountClassSQL; sums are numeric.
	rows, err := a.db.Query(ctx, `SELECT GROUPING(g.stage)=1, g.stage, count(*),
 coalesce(sum(g.amount) FILTER (WHERE g.class='KNOWN'),0)::numeric(20,2)::text,
 count(*) FILTER (WHERE g.class='KNOWN'),
 count(*) FILTER (WHERE g.class='KNOWN' AND g.amount=0),
 count(*) FILTER (WHERE g.class='KNOWN' AND g.implicit),
 count(*) FILTER (WHERE g.class='UNKNOWN'),
 count(*) FILTER (WHERE g.class='LEGACY_UNCONFIRMED'),
 count(*) FILTER (WHERE g.class='INVALID')
 FROM (SELECT CASE WHEN p.sales_stage IN (`+stageCodesSQL+`) THEN p.sales_stage ELSE '' END AS stage,
   p.estimated_amount AS amount, `+amountClassSQL("p")+` AS class,
   coalesce(p.document->'knowledge'->>'estimatedAmount','')='' AS implicit
   FROM intake_projects p`+where+`) g
 GROUP BY GROUPING SETS ((g.stage),())`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	groups := map[string]StageCount{}
	for rows.Next() {
		var total bool
		var stage *string
		var c StageCount
		m := &c.Amount
		if err = rows.Scan(&total, &stage, &c.Count, &m.KnownTotal, &m.KnownCount, &m.KnownZeroCount, &m.KnownImplicitCount, &m.UnknownCount, &m.LegacyUnconfirmedCount, &m.InvalidCount); err != nil {
			return out, err
		}
		if total {
			out.Total, out.Amount = c.Count, c.Amount
			continue
		}
		groups[*stage] = c
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	for i, code := range stages {
		c, ok := groups[code]
		if !ok {
			c = StageCount{Amount: zeroAmount()}
		}
		c.Code = code
		out.Stages[i] = c
	}
	if c, ok := groups[""]; ok {
		out.Unclassified, out.UnclassifiedAmount = c.Count, c.Amount
	}
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
