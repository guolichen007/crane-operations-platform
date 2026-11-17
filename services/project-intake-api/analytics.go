package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// V1.1 R2: layered statistics. Every list, pipeline and statistics request
// may narrow the caller's projectScope with a server-checked selection:
//
//	scope=mine              owner = the caller (sales, supervisor)
//	scope=team[&teamId=]    supervisor: own team (teamId absent or equal);
//	                        director/admin: one team (teamId required)
//	scope=member&memberId=  one member (staff id): supervisor only members of
//	                        the own team (sales or supervisor, incl. self);
//	                        director/admin any sales member or supervisor
//	scope=all               director/admin
//
// The selection is always ANDed with projectScope, so it can only narrow.
// A scope the role does not have is 403 SCOPE_FORBIDDEN; a member or team the
// caller cannot see is 404 SCOPE_NOT_FOUND, without saying whether it exists.

type scopeSel struct {
	Scope string `json:"scope"`
	Owner string `json:"-"`
	Team  string `json:"teamId,omitempty"`
	// Member: the resolved member (id and display name) for scope=member.
	MemberID   string `json:"memberId,omitempty"`
	MemberName string `json:"memberName,omitempty"`
	MemberRole string `json:"memberRole,omitempty"`
}

var scopeKeys = []string{"scope", "memberId", "teamId"}

func availableScopes(role string) []string {
	switch role {
	case salesRole:
		return []string{"mine"}
	case managerRole:
		return []string{"team", "mine", "member"}
	case directorRole, adminRole:
		return []string{"all", "team", "member"}
	}
	return []string{}
}

var (
	scopeForbidden = staffProblem{403, "SCOPE_FORBIDDEN", "当前账号不能查看该统计范围"}
	scopeNotFound  = staffProblem{404, "SCOPE_NOT_FOUND", "成员或团队不存在或不在你的范围内"}
)

// resolveScope validates scope/memberId/teamId (removing them from params) and
// returns the narrowing selection. Without scope the caller's default scope
// applies unchanged (pre-V1.1 behaviour).
func resolveScope(ctx context.Context, q querier, u User, params map[string]string) (scopeSel, error) {
	scope, member, team := params["scope"], params["memberId"], params["teamId"]
	for _, k := range scopeKeys {
		delete(params, k)
	}
	sel := scopeSel{Scope: scope}
	if scope == "" {
		if member != "" || team != "" {
			return sel, listError("成员或团队筛选需要同时指定统计范围")
		}
		return sel, nil
	}
	if !contains([]string{"mine", "team", "member", "all"}, scope) {
		return sel, listError("统计范围不正确")
	}
	if !contains(availableScopes(u.Role), scope) {
		return sel, scopeForbidden
	}
	if member != "" && scope != "member" || team != "" && scope != "team" {
		return sel, listError("成员或团队筛选与统计范围不一致")
	}
	switch scope {
	case "mine":
		sel.Owner = u.Subject
	case "team":
		if u.Role == managerRole {
			if team != "" && team != u.TeamID {
				return sel, scopeNotFound
			}
			team = u.TeamID
		}
		if !uuidPattern.MatchString(team) {
			if u.Role == managerRole {
				return sel, scopeNotFound
			}
			return sel, listError("请选择团队")
		}
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1)`, team).Scan(&ok); err != nil {
			return sel, err
		}
		if !ok {
			return sel, scopeNotFound
		}
		sel.Team = team
	case "member":
		if !uuidPattern.MatchString(member) {
			return sel, scopeNotFound
		}
		err := q.QueryRow(ctx, `SELECT u.subject,u.display_name,u.role FROM app_users u WHERE u.staff_id=$1
 AND u.role IN ('baobei_sales','baobei_manager')
 AND ($2<>'baobei_manager' OR (u.team_id IS NOT NULL AND u.team_id::text=$3))`, member, u.Role, u.TeamID).Scan(&sel.Owner, &sel.MemberName, &sel.MemberRole)
		if errors.Is(err, pgx.ErrNoRows) {
			return sel, scopeNotFound
		}
		if err != nil {
			return sel, err
		}
		sel.MemberID = member
	}
	return sel, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// sortMembers: the caller first, then supervisors, then by name; "others" last.
func sortMembers(ms []MemberRow, self string) {
	rank := func(m MemberRow) int {
		if m.Others {
			return 3
		}
		if m.Role == managerRole {
			return 1
		}
		return 2
	}
	sort.SliceStable(ms, func(i, j int) bool {
		if rank(ms[i]) != rank(ms[j]) {
			return rank(ms[i]) < rank(ms[j])
		}
		return ms[i].Name < ms[j].Name
	})
}

// narrowSQL appends the selection to a projectFilters WHERE clause.
func (s scopeSel) narrow(where string, args []any) (string, []any) {
	if s.Owner != "" {
		args = append(args, s.Owner)
		where += ` AND p.owner_subject=$` + itoa(len(args))
	}
	if s.Team != "" {
		args = append(args, s.Team)
		where += ` AND p.team_id::text=$` + itoa(len(args))
	}
	return where, args
}

// AnalyticsOverview: one REPEATABLE READ snapshot for the stage/amount summary
// and the status and follow-up counts of the same selection.
type AnalyticsOverview struct {
	StageSummary
	Selection       scopeSel       `json:"selection"`
	AvailableScopes []string       `json:"availableScopes"`
	Counts          map[string]int `json:"counts"`
	Groups          []GroupCount   `json:"groups"`
	LastUpdatedAt   *time.Time     `json:"lastUpdatedAt"`
}

// GroupCount: the six display groups of the pipeline (V1.1), summed from the
// 12 stored stage codes; nothing is stored per group.
type GroupCount struct {
	Code   string   `json:"code"`
	Stages []string `json:"stages"`
	Count  int      `json:"count"`
}

var stageGroups = []struct {
	Code   string
	Stages []string
}{
	{"G1", []string{"L0_LEAD", "L1_REQUIREMENT"}},
	{"G2", []string{"L2_TECHNICAL", "L3_SITE_SURVEY"}},
	{"G3", []string{"L4_SOLUTION", "L5_QUOTATION"}},
	{"G4", []string{"L6_TENDER", "L7_COMMERCIAL"}},
	{"G5", []string{"L8_PENDING_CONTRACT"}},
	{"G6", []string{"WON", "LOST", "HOLD"}},
}

func groupStages(code string) []string {
	for _, g := range stageGroups {
		if g.Code == code {
			return g.Stages
		}
	}
	return nil
}

func groupsOf(stagesOut []StageCount) []GroupCount {
	by := map[string]int{}
	for _, s := range stagesOut {
		by[s.Code] = s.Count
	}
	out := make([]GroupCount, 0, len(stageGroups))
	for _, g := range stageGroups {
		c := GroupCount{Code: g.Code, Stages: g.Stages}
		for _, s := range g.Stages {
			c.Count += by[s]
		}
		out = append(out, c)
	}
	return out
}

func (a *App) analyticsOverview(ctx context.Context, u User, params map[string]string) (AnalyticsOverview, error) {
	out := AnalyticsOverview{AvailableScopes: availableScopes(u.Role), Counts: map[string]int{}}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	sel, err := resolveScope(ctx, tx, u, params)
	if err != nil {
		return out, err
	}
	out.Selection = sel
	if out.Selection.Scope == "" {
		out.Selection.Scope = availableScopes(u.Role)[0]
	}
	out.StageSummary, err = a.stageSummaryIn(ctx, tx, u, params, sel)
	if err != nil {
		return out, err
	}
	where, args, err := projectFilters(u, map[string]string{"q": params["q"], "status": params["status"], "province": params["province"], "updatedAfter": params["updatedAfter"]}, out.AsOf)
	if err != nil {
		return out, err
	}
	where, args = sel.narrow(where, args)
	args = append(args, out.AsOf, out.AsOf.Add(7*24*time.Hour))
	n := len(args)
	var draft, submitted, review, approved, returned, rejected, other, overdue, upcoming, unscheduled, unassigned int
	err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE p.intake_status='DRAFT'),
 count(*) FILTER (WHERE p.intake_status='SUBMITTED'), count(*) FILTER (WHERE p.intake_status='UNDER_REVIEW'),
 count(*) FILTER (WHERE p.intake_status='APPROVED'), count(*) FILTER (WHERE p.intake_status='RETURNED'),
 count(*) FILTER (WHERE p.intake_status='REJECTED'),
 count(*) FILTER (WHERE p.intake_status NOT IN ('DRAFT','SUBMITTED','UNDER_REVIEW','APPROVED','RETURNED','REJECTED')),
 count(*) FILTER (WHERE p.next_followup_at<$`+itoa(n-1)+`),
 count(*) FILTER (WHERE p.next_followup_at>=$`+itoa(n-1)+` AND p.next_followup_at<$`+itoa(n)+`),
 count(*) FILTER (WHERE p.next_followup_at IS NULL),
 count(*) FILTER (WHERE p.owner_subject IS NULL), max(p.updated_at)
 FROM intake_projects p`+where, args...).Scan(&draft, &submitted, &review, &approved, &returned, &rejected, &other, &overdue, &upcoming, &unscheduled, &unassigned, &out.LastUpdatedAt)
	if err != nil {
		return out, err
	}
	for k, v := range map[string]int{"draft": draft, "submitted": submitted, "underReview": review, "approved": approved, "returned": returned, "rejected": rejected, "otherStatus": other, "pendingReview": submitted + review, "overdue": overdue, "upcoming7d": upcoming, "unscheduled": unscheduled, "unassignedDrafts": unassigned} {
		out.Counts[k] = v
	}
	out.Groups = groupsOf(out.Stages)
	return out, tx.Commit(ctx)
}

// MemberRow: one owner in a team breakdown. Members are the team's sales
// members and supervisors that are not archived or revoked; owners of team
// projects outside that set (moved away, archived) are summed in one "others"
// row, so sum(rows) == team total always holds.
type MemberRow struct {
	MemberID  string         `json:"memberId,omitempty"`
	Name      string         `json:"name"`
	Role      string         `json:"role,omitempty"`
	Status    string         `json:"status,omitempty"`
	Others    bool           `json:"others,omitempty"`
	Total     int            `json:"total"`
	Approved  int            `json:"approved"`
	Pending   int            `json:"pendingReview"`
	Overdue   int            `json:"overdue"`
	Stages    map[string]int `json:"stages"`
	Amount    AmountSummary  `json:"amount"`
	UpdatedAt *time.Time     `json:"lastUpdatedAt"`
}
type TeamRow struct {
	TeamID   string `json:"teamId"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Total    int    `json:"total"`
	Approved int    `json:"approved"`
	Pending  int    `json:"pendingReview"`
	Overdue  int    `json:"overdue"`
}
type MembersReport struct {
	AsOf    time.Time   `json:"asOf"`
	TeamID  string      `json:"teamId,omitempty"`
	Members []MemberRow `json:"members,omitempty"`
	Teams   []TeamRow   `json:"teams,omitempty"`
	Total   int         `json:"total"`
}

func (a *App) analyticsMembers(ctx context.Context, u User, team string) (MembersReport, error) {
	out := MembersReport{}
	if u.Role == salesRole {
		return out, scopeForbidden
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&out.AsOf); err != nil {
		return out, err
	}
	out.AsOf = out.AsOf.UTC()
	scoped := ` FROM intake_projects p WHERE p.discarded_at IS NULL AND ` + projectScope("p")
	if u.Role != managerRole && team == "" {
		// Directors and administrators: one row per team (assigned projects).
		rows, err := tx.Query(ctx, `SELECT t.id::text,t.name,t.status,count(p.id),count(p.id) FILTER (WHERE p.intake_status='APPROVED'),
 count(p.id) FILTER (WHERE p.intake_status IN ('SUBMITTED','UNDER_REVIEW')),count(p.id) FILTER (WHERE p.next_followup_at<$4)
 FROM teams t LEFT JOIN (SELECT p.*`+scoped+`) p ON p.team_id=t.id GROUP BY t.id,t.name,t.status ORDER BY t.status,t.name,t.id`, u.Subject, u.Role, u.TeamID, out.AsOf)
		if err != nil {
			return out, err
		}
		defer rows.Close()
		for rows.Next() {
			var r TeamRow
			if err = rows.Scan(&r.TeamID, &r.Name, &r.Status, &r.Total, &r.Approved, &r.Pending, &r.Overdue); err != nil {
				return out, err
			}
			out.Teams = append(out.Teams, r)
			out.Total += r.Total
		}
		if err = rows.Err(); err != nil {
			return out, err
		}
		rows.Close()
		var assigned int
		if err = tx.QueryRow(ctx, `SELECT count(*)`+scoped+` AND p.owner_subject IS NOT NULL`, u.Subject, u.Role, u.TeamID).Scan(&assigned); err != nil {
			return out, err
		}
		out.Total = assigned
		return out, tx.Commit(ctx)
	}
	if u.Role == managerRole {
		if team != "" && team != u.TeamID {
			return out, scopeNotFound
		}
		team = u.TeamID
	}
	if !uuidPattern.MatchString(team) {
		return out, scopeNotFound
	}
	var ok bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1)`, team).Scan(&ok); err != nil {
		return out, err
	}
	if !ok {
		return out, scopeNotFound
	}
	out.TeamID = team
	// One statement: team projects in scope grouped by owner, joined to the
	// member list; owners outside the member list fall into the "others" row.
	rows, err := tx.Query(ctx, `WITH members AS (
  SELECT u.staff_id::text AS id,u.subject,u.display_name,u.role,u.staff_status FROM app_users u
  WHERE u.team_id::text=$4 AND u.role IN ('baobei_sales','baobei_manager') AND u.staff_status NOT IN ('ARCHIVED','REVOKED')),
 proj AS (SELECT p.*,`+amountClassSQL("p")+` AS class`+scoped+` AND p.team_id::text=$4),
 keyed AS (SELECT CASE WHEN m.id IS NULL THEN '' ELSE m.id END AS mid,p.* FROM proj p LEFT JOIN members m ON m.subject=p.owner_subject)
 SELECT m.id,m.display_name,m.role,m.staff_status,false,
  count(k.id),count(k.id) FILTER (WHERE k.intake_status='APPROVED'),count(k.id) FILTER (WHERE k.intake_status IN ('SUBMITTED','UNDER_REVIEW')),
  count(k.id) FILTER (WHERE k.next_followup_at<$5),
  round(coalesce(sum(k.estimated_amount) FILTER (WHERE k.class='KNOWN'),0),2)::text,
  count(k.id) FILTER (WHERE k.class='KNOWN'),count(k.id) FILTER (WHERE k.class='UNKNOWN'),
  count(k.id) FILTER (WHERE k.class='LEGACY_UNCONFIRMED'),count(k.id) FILTER (WHERE k.class='INVALID'),max(k.updated_at)
 FROM members m LEFT JOIN keyed k ON k.mid=m.id GROUP BY m.id,m.display_name,m.role,m.staff_status
 UNION ALL
 SELECT '', '', '', '', true,
  count(k.id),count(k.id) FILTER (WHERE k.intake_status='APPROVED'),count(k.id) FILTER (WHERE k.intake_status IN ('SUBMITTED','UNDER_REVIEW')),
  count(k.id) FILTER (WHERE k.next_followup_at<$5),
  round(coalesce(sum(k.estimated_amount) FILTER (WHERE k.class='KNOWN'),0),2)::text,
  count(k.id) FILTER (WHERE k.class='KNOWN'),count(k.id) FILTER (WHERE k.class='UNKNOWN'),
  count(k.id) FILTER (WHERE k.class='LEGACY_UNCONFIRMED'),count(k.id) FILTER (WHERE k.class='INVALID'),max(k.updated_at)
 FROM keyed k WHERE k.mid=''`, u.Subject, u.Role, u.TeamID, team, out.AsOf)
	if err != nil {
		return out, err
	}
	index := map[string]int{}
	for rows.Next() {
		var r MemberRow
		if err = rows.Scan(&r.MemberID, &r.Name, &r.Role, &r.Status, &r.Others, &r.Total, &r.Approved, &r.Pending, &r.Overdue,
			&r.Amount.KnownTotal, &r.Amount.KnownCount, &r.Amount.UnknownCount, &r.Amount.LegacyUnconfirmedCount, &r.Amount.InvalidCount, &r.UpdatedAt); err != nil {
			rows.Close()
			return out, err
		}
		if r.Others {
			r.Name = "其他负责人（已调离或已归档）"
			if r.Total == 0 {
				continue
			}
		}
		r.Stages = map[string]int{}
		index[r.MemberID] = len(out.Members)
		out.Members = append(out.Members, r)
		out.Total += r.Total
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Stage distribution per member (same scope), one statement.
	srows, err := tx.Query(ctx, `SELECT coalesce((SELECT u.staff_id::text FROM app_users u WHERE u.subject=p.owner_subject AND u.team_id::text=$4
  AND u.role IN ('baobei_sales','baobei_manager') AND u.staff_status NOT IN ('ARCHIVED','REVOKED')),''),p.sales_stage,count(*)`+scoped+` AND p.team_id::text=$4 GROUP BY 1,2`,
		u.Subject, u.Role, u.TeamID, team)
	if err != nil {
		return out, err
	}
	defer srows.Close()
	for srows.Next() {
		var id, stage string
		var n int
		if err = srows.Scan(&id, &stage, &n); err != nil {
			return out, err
		}
		if i, ok := index[id]; ok {
			out.Members[i].Stages[stage] = n
		}
	}
	if err = srows.Err(); err != nil {
		return out, err
	}
	srows.Close()
	sortMembers(out.Members, u.Subject)
	return out, tx.Commit(ctx)
}

func (a *App) analyticsHandler(w http.ResponseWriter, r *http.Request, u User) error {
	params := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			fail(w, 400, "INVALID_FILTER", "筛选条件重复："+k)
			return nil
		}
		params[k] = v[0]
	}
	var out any
	var err error
	switch r.URL.Path {
	case "/api/v1/analytics/overview":
		for k := range params {
			if !stageSummaryParams[k] {
				fail(w, 400, "INVALID_FILTER", "统计不支持的筛选条件："+k)
				return nil
			}
		}
		out, err = a.analyticsOverview(r.Context(), u, params)
	case "/api/v1/analytics/members":
		for k := range params {
			if k != "teamId" {
				fail(w, 400, "INVALID_FILTER", "成员统计不支持的筛选条件："+k)
				return nil
			}
		}
		out, err = a.analyticsMembers(r.Context(), u, params["teamId"])
	default:
		fail(w, 404, "NOT_FOUND", "接口不存在")
		return nil
	}
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
