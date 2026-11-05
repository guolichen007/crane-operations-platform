package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"
)

type GovernanceEvent struct {
	Actor     string    `json:"actor"`
	ActorName string    `json:"actorName"`
	FromName  string    `json:"fromOwnerName,omitempty"`
	ToName    string    `json:"toOwnerName,omitempty"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason"`
	FromOwner string    `json:"fromOwner,omitempty"`
	ToOwner   string    `json:"toOwner,omitempty"`
	At        time.Time `json:"at"`
}
type decisionInput struct {
	RelatedVersions map[string]int `json:"relatedVersions,omitempty"`
	Version         int            `json:"version"`
	Reason          string         `json:"reason"`
	TargetSubject   string         `json:"targetSubject,omitempty"`
	SalesStage      string         `json:"salesStage,omitempty"`
	Decision        string         `json:"decision,omitempty"`
	CaseID          string         `json:"caseId,omitempty"`
}
type Comparison struct {
	ID         string   `json:"id"`
	Score      int      `json:"score"`
	Risk       string   `json:"risk"`
	Signals    []string `json:"signals"`
	Decision   string   `json:"decision"`
	Candidate  *Project `json:"candidate,omitempty"`
	Restricted bool     `json:"restricted"`
}

func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic("internal JSON encoding")
	}
	return b
}
func manager(u User) bool { return u.Role == "baobei_manager" || u.Role == "baobei_director" }
func validStage(from, to, status string, reason bool) bool {
	if from == to || !contains(stages, to) {
		return false
	}
	if editable(status) {
		return true
	}
	if !contains([]string{"SUBMITTED", "UNDER_REVIEW", "APPROVED"}, status) {
		return false
	}
	if from == "WON" || from == "LOST" {
		return false
	}
	if from == "HOLD" {
		return reason && to == "L1_REQUIREMENT"
	}
	if to == "HOLD" || to == "LOST" {
		return reason
	}
	if to == "WON" {
		return reason && from == "L8_PENDING_CONTRACT"
	}
	for i, s := range stages[:9] {
		if s == from {
			return i < 8 && stages[i+1] == to
		}
	}
	return false
}
func (a *App) governanceHistory(ctx context.Context, p *Project) error {
	rows, e := a.db.Query(ctx, `SELECT x.actor,x.action,x.reason,x.from_owner,x.to_owner,x.at,`+displayName("x.actor")+`,`+displayName("x.from_owner")+`,`+displayName("x.to_owner")+` FROM (
 SELECT subject actor,decision action,reason,'' AS from_owner,'' AS to_owner,created_at at FROM approvals WHERE project_id=$1 UNION ALL
 SELECT coalesce(actor_subject,''),'ownership.transfer',reason,coalesce(from_subject,''),to_subject,created_at FROM ownership_history WHERE project_id=$1) x ORDER BY x.at DESC`, p.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	p.GovernanceHistory = []GovernanceEvent{}
	for rows.Next() {
		var ev GovernanceEvent
		if e = rows.Scan(&ev.Actor, &ev.Action, &ev.Reason, &ev.FromOwner, &ev.ToOwner, &ev.At, &ev.ActorName, &ev.FromName, &ev.ToName); e != nil {
			return e
		}
		p.GovernanceHistory = append(p.GovernanceHistory, ev)
	}
	return rows.Err()
}
func (a *App) comparison(ctx context.Context, q querier, p Project, u User) ([]Comparison, error) {
	rows, e := q.Query(ctx, `SELECT id::text,candidate_id::text,score,risk,matched_signals,decision FROM duplicate_cases WHERE project_id=$1 AND (submission_revision=$2 OR decision<>'PENDING') ORDER BY created_at DESC,id`, p.ID, p.SubmissionRevision)
	if e != nil {
		return nil, e
	}
	out := []Comparison{}
	ids := []string{}
	for rows.Next() {
		var c Comparison
		var id string
		var b []byte
		if e = rows.Scan(&c.ID, &id, &c.Score, &c.Risk, &b, &c.Decision); e != nil {
			rows.Close()
			return nil, e
		}
		if e = json.Unmarshal(b, &c.Signals); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, c)
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i, id := range ids {
		candidate, e := get(ctx, q, id, false)
		if e != nil {
			return nil, e
		}
		if canRead(u, candidate) {
			out[i].Candidate = &candidate
		} else {
			out[i].Restricted = true
		}
	}
	return out, nil
}
func (a *App) governance(w http.ResponseWriter, r *http.Request, u User) (bool, error) {
	path, method, ctx := r.URL.Path, r.Method, r.Context()
	if path == "/api/v1/reviews" && method == "GET" {
		if !(ProjectPolicy{u}).Manage() {
			fail(w, 403, "FORBIDDEN", "没有审核权限")
			return true, nil
		}
		params := map[string]string{"review": "pending", "page": r.URL.Query().Get("page"), "pageSize": "20"}
		kind := r.URL.Query().Get("kind")
		if kind == "duplicates" {
			params["status"] = "UNDER_REVIEW"
		}
		if kind == "protection" {
			params["review"] = "protection"
		}
		items, total, e := a.list(ctx, u, params)
		if e != nil {
			return true, e
		}
		respond(w, 200, map[string]any{"items": items, "total": total})
		return true, nil
	}
	if path == "/api/v1/ownership-candidates" && method == "GET" {
		if !(ProjectPolicy{u}).Manage() {
			fail(w, 403, "FORBIDDEN", "没有归属管理权限")
			return true, nil
		}
		id := r.URL.Query().Get("projectId")
		if !uuidPattern.MatchString(id) {
			fail(w, 400, "INVALID_INPUT", "请选择项目")
			return true, nil
		}
		p, e := get(ctx, a.db, id, false)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return true, e
		}
		if e != nil || !canRead(u, p) {
			fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
			return true, nil
		}
		rows, e := a.db.Query(ctx, `SELECT subject,display_name FROM app_users WHERE active AND role='baobei_sales' AND ($1 IN ('baobei_director','baobei_admin') OR team_id::text=$2) ORDER BY display_name`, u.Role, u.TeamID)
		if e != nil {
			return true, e
		}
		defer rows.Close()
		items := []map[string]string{}
		for rows.Next() {
			var subject, name string
			if e = rows.Scan(&subject, &name); e != nil {
				return true, e
			}
			items = append(items, map[string]string{"subject": subject, "name": name})
		}
		if e = rows.Err(); e != nil {
			return true, e
		}
		respond(w, 200, map[string]any{"items": items})
		return true, nil
	}
	const prefix = "/api/v1/projects/"
	if !strings.HasPrefix(path, prefix) {
		return false, nil
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 2 || !contains([]string{"approve", "return", "reject", "ownership-transfer", "stage-change", "duplicate-decisions", "duplicate-comparison"}, parts[1]) {
		return false, nil
	}
	id, action := parts[0], parts[1]
	if !uuidPattern.MatchString(id) {
		fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
		return true, nil
	}
	if action == "duplicate-comparison" && method == "GET" {
		if !(ProjectPolicy{u}).Manage() {
			fail(w, 403, "FORBIDDEN", "没有重复审核权限")
			return true, nil
		}
		p, e := get(ctx, a.db, id, false)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return true, e
		}
		if e != nil || !canRead(u, p) {
			fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
			return true, nil
		}
		cases, e := a.comparison(ctx, a.db, p, u)
		if e != nil {
			return true, e
		}
		respond(w, 200, map[string]any{"project": p, "cases": cases})
		return true, nil
	}
	if method != "POST" {
		fail(w, 405, "METHOD_NOT_ALLOWED", "操作不支持")
		return true, nil
	}
	if action != "stage-change" && !(ProjectPolicy{u}).Manage() {
		fail(w, 403, "FORBIDDEN", "没有主管操作权限")
		return true, nil
	}
	var in decisionInput
	if !decode(w, r, &in) {
		return true, nil
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Reason) > 3000 {
		fail(w, 400, "INVALID_INPUT", "请填写不超过1000字的操作原因")
		return true, nil
	}
	tx, e := a.beginBusiness(ctx, u, 0)
	if e != nil {
		return true, e
	}
	defer tx.Rollback(ctx)
	var related []Project
	if contains([]string{"approve", "return", "reject", "duplicate-decisions"}, action) {
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727102)`); e != nil {
			return true, e
		}
		related, e = relatedReviewProjects(ctx, tx, id)
		if e != nil {
			return true, e
		}
	}
	p, e := get(ctx, tx, id, true)
	if e != nil {
		if !errors.Is(e, pgx.ErrNoRows) {
			return true, e
		}
		fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
		return true, nil
	}
	if !canRead(u, p) {
		fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
		return true, nil
	}
	if in.Version != p.Version {
		fail(w, 409, "VERSION_CONFLICT", "项目已更新，请重新加载后重试")
		return true, nil
	}
	if e = enforceProject(u, p, action); e != nil {
		return true, e
	}
	if contains([]string{"approve", "return", "reject", "duplicate-decisions"}, action) {
		if e = reviewIndependence(ctx, tx, u, p); e != nil {
			return true, e
		}
		if action == "duplicate-decisions" {
			for _, candidate := range related {
				if candidate.ID == p.ID {
					continue
				}
				if e = candidateIndependence(ctx, tx, u, candidate); e != nil {
					return true, e
				}
			}
		}
	}
	event := "review." + action
	details := map[string]any{"reason": in.Reason}
	switch action {
	case "stage-change":
		if !canWrite(u, p) {
			fail(w, 403, "FORBIDDEN", "仅负责人可以修改销售阶段")
			return true, nil
		}
		if !validStage(p.SalesStage, in.SalesStage, p.IntakeStatus, true) {
			fail(w, 409, "INVALID_STATE", "阶段只能逐步向前；暂停仅可回到需求确认，结束阶段不可重新激活")
			return true, nil
		}
		details["fromStage"] = p.SalesStage
		details["toStage"] = in.SalesStage
		p.SalesStage = in.SalesStage
		p.Input.Version = p.Version + 1
		if _, e = tx.Exec(ctx, `UPDATE intake_projects SET sales_stage=$2,document=$3,version=version+1,updated_at=now() WHERE id=$1`, id, p.SalesStage, mustJSON(p.Input)); e != nil {
			return true, e
		}
		event = "project.stage-change"
	case "ownership-transfer":
		if !contains([]string{"DRAFT", "RETURNED", "SUBMITTED", "UNDER_REVIEW", "APPROVED"}, p.IntakeStatus) {
			fail(w, 409, "INVALID_STATE", "当前状态不允许转移归属")
			return true, nil
		}
		var teamID string

		teamID, e = activeOwner(ctx, tx, in.TargetSubject)
		if e != nil {
			return true, e
		}
		if in.TargetSubject == p.OwnerSubject || u.Role == managerRole && teamID != u.TeamID {
			return true, problem(400, "INVALID_TARGET")
		}

		if _, e = tx.Exec(ctx, `INSERT INTO ownership_history(project_id,from_subject,to_subject,reason,actor_subject) VALUES($1,NULLIF($2,''),$3,$4,$5)`, id, p.OwnerSubject, in.TargetSubject, in.Reason, u.Subject); e != nil {
			return true, e
		}
		details["fromOwner"] = p.OwnerSubject
		details["toOwner"] = in.TargetSubject
		if _, e = tx.Exec(ctx, `UPDATE intake_projects SET owner_subject=$2,team_id=$3::uuid,version=version+1,updated_at=now() WHERE id=$1`, id, in.TargetSubject, teamID); e != nil {
			return true, e
		}
		event = "ownership.transfer"
	default:
		if !contains([]string{"SUBMITTED", "UNDER_REVIEW"}, p.IntakeStatus) {
			fail(w, 409, "INVALID_STATE", "仅已提交或待审核报备可以审核")
			return true, nil
		}
		status := ""
		decision := strings.ToUpper(action)
		switch action {
		case "approve":
			var pending int
			if e = tx.QueryRow(ctx, `SELECT count(*) FROM duplicate_cases WHERE project_id=$1 AND decision='PENDING' AND submission_revision=(SELECT submission_revision FROM intake_projects WHERE id=$1)`, id).Scan(&pending); e != nil {
				return true, e
			}
			if pending > 0 {
				fail(w, 409, "DUPLICATE_DECISION_REQUIRED", "请先完成重复项目对比与裁决")
				return true, nil
			}
			status = "APPROVED"
		case "return":
			status = "RETURNED"
		case "reject":
			status = "REJECTED"
		case "duplicate-decisions":
			if !uuidPattern.MatchString(in.CaseID) || !contains([]string{"CONFIRMED_DUPLICATE", "DISTINCT_APPROVED"}, in.Decision) {
				fail(w, 400, "INVALID_INPUT", "请选择重复候选与处理结论")
				return true, nil
			}
			if len(in.RelatedVersions) != len(related)-1 {
				return true, problem(409, "RELATED_VERSION_CONFLICT")
			}
			for _, candidate := range related {
				if candidate.ID != p.ID && in.RelatedVersions[candidate.ID] != candidate.Version {
					return true, problem(409, "RELATED_VERSION_CONFLICT")
				}
			}
			cases, e := a.comparison(ctx, tx, p, u)
			if e != nil {
				return true, e
			}
			found := false
			for _, c := range cases {
				if c.ID == in.CaseID && c.Decision == "PENDING" {
					found = true
					if c.Restricted {
						fail(w, 403, "FORBIDDEN", "跨团队重复需销售总监处理")
						return true, nil
					}
				}
				if in.Decision == "DISTINCT_APPROVED" && c.Decision == "PENDING" && c.Restricted {
					fail(w, 403, "FORBIDDEN", "存在跨团队候选，需销售总监核验全部候选")
					return true, nil
				}
			}
			if !found {
				fail(w, 409, "INVALID_STATE", "重复候选已处理或不属于该报备")
				return true, nil
			}
			decision = in.Decision
			status = "REJECTED"
			event = "duplicate.confirm"
			where := ` AND id=$6::uuid`
			args := []any{id, in.Decision, in.Reason, u.Subject, time.Now().UTC(), in.CaseID}
			if in.Decision == "DISTINCT_APPROVED" {
				status = "APPROVED"
				event = "duplicate.dismiss"
				where = ""
				args = args[:5]
			}
			if _, e = tx.Exec(ctx, `UPDATE duplicate_cases SET decision=$2,reason=$3,decided_by_subject=$4,decided_at=$5 WHERE project_id=$1 AND decision='PENDING' AND submission_revision=(SELECT submission_revision FROM intake_projects WHERE id=$1)`+where, args...); e != nil {
				return true, e
			}
			details["decision"] = decision
			details["caseId"] = in.CaseID
		}
		if _, e = tx.Exec(ctx, `UPDATE intake_projects SET intake_status=$2,version=version+1,updated_at=now() WHERE id=$1`, id, status); e != nil {
			return true, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO approvals(project_id,subject,decision,reason,submission_revision) VALUES($1,$2,$3,$4,$5)`, id, u.Subject, decision, in.Reason, p.SubmissionRevision); e != nil {
			return true, e
		}
	}
	p, e = get(ctx, tx, id, false)
	if e != nil {
		return true, e
	}
	if action == "ownership-transfer" || action == "stage-change" {
		kind := "ASSIGN"
		if action == "stage-change" {
			kind = "STAGE"
		}
		if e = participation(ctx, tx, p, u, kind); e != nil {
			return true, e
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_events(project_id,subject,action,revision,details) VALUES($1,$2,$3,$4,$5)`, id, u.Subject, event, p.Version, mustJSON(details)); e != nil {
		return true, e
	}
	if e = tx.Commit(ctx); e != nil {
		return true, e
	}
	if e = a.projectHints(ctx, a.db, u, &p); e != nil {
		return true, e
	}
	respond(w, 200, p)
	return true, nil
}
