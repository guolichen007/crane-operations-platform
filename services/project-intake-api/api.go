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

func (a *App) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", randomToken())
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.URL.Path == "/health/live" {
			respond(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/health/ready" {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if a.db.Ping(ctx) != nil || a.staffDB != nil && a.staffDB.Ping(ctx) != nil {
				fail(w, 503, "DB_UNAVAILABLE", "数据库未就绪")
			} else {
				respond(w, 200, map[string]bool{"ok": true})
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			a.auth(w, r)
			return
		}
		u, e := a.user(r)
		if e != nil {
			a.sessionFailure(w, e)
			return
		}
		if r.Method != "GET" && !a.csrf(w, r, u) {
			return
		}
		if e = a.api(w, r, u); e != nil && !staffError(w, e) {
			fail(w, 503, "SERVICE_UNAVAILABLE", "服务暂时不可用，请保留输入并重试")
		}
	})
}
func (a *App) api(w http.ResponseWriter, r *http.Request, u User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	path, method := r.URL.Path, r.Method
	if path == "/api/v1/staff" || strings.HasPrefix(path, "/api/v1/staff/") || path == "/api/v1/teams" || strings.HasPrefix(path, "/api/v1/teams/") {
		return a.staff(w, r, u)
	}
	if handled, e := a.governance(w, r, u); handled {
		return e
	}
	if path == "/api/v1/me" && method == "GET" {
		u.BusinessActions = map[string]ActionHint{"project.create": hint("BUSINESS_PERMISSION_DENIED")}
		if (ProjectPolicy{u}).Create() {
			u.BusinessActions["project.create"] = hint("")
		}
		u.BusinessActions["project.read"] = hint("")
		u.BusinessActions["project.review"] = hint("BUSINESS_PERMISSION_DENIED")
		if (ProjectPolicy{u}).Manage() {
			u.BusinessActions["project.review"] = hint("")
		}
		// Navigation hints only; every endpoint re-authorizes on the server.
		u.BusinessActions["project.assign"] = u.BusinessActions["project.review"]
		u.BusinessActions["project.unassigned"] = hint("BUSINESS_PERMISSION_DENIED")
		u.BusinessActions["staff.manage"] = hint("BUSINESS_PERMISSION_DENIED")
		u.BusinessActions["team.manage"] = hint("BUSINESS_PERMISSION_DENIED")
		if u.Role == adminRole {
			u.BusinessActions["project.unassigned"] = hint("")
			u.BusinessActions["team.manage"] = hint("")
		}
		if staffRole(u.Role) {
			u.BusinessActions["staff.manage"] = hint("")
		}
		respond(w, 200, u)
		return nil
	}
	if path == "/api/v1/reference-values" && method == "GET" {
		respond(w, 200, map[string]any{"salesStages": stages, "informationSources": sources, "confidenceLevels": confidence, "requirements": requirements, "activityTypes": activityTypes})
		return nil
	}
	if path == "/api/v1/duplicate-checks" && method == "POST" {
		if u.Role != salesRole && u.Role != adminRole {
			fail(w, 403, "FORBIDDEN", "没有报备权限")
			return nil
		}
		var in Input
		if !decode(w, r, &in) {
			return nil
		}
		if e := validateInput(&in, false); e != nil {
			fail(w, 400, "INVALID_INPUT", e.Error())
			return nil
		}
		if in.Customer == "" || in.ProjectName == "" || in.Location.Province == "" || in.Location.City == "" {
			respond(w, 200, map[string]any{"items": []Duplicate{}})
			return nil
		}
		ds, e := a.duplicates(ctx, a.db, in, "")
		if e != nil {
			return e
		}
		respond(w, 200, map[string]any{"items": redact(ds, u)})
		return nil
	}
	if path == "/api/v1/workspace-summary" && method == "GET" {
		return a.summaryHandler(w, r, u)
	}
	if strings.HasPrefix(path, "/api/v1/project-create-receipts/") && method == "GET" {
		return a.createReceipt(w, r, u, strings.TrimPrefix(path, "/api/v1/project-create-receipts/"))
	}
	if path == "/api/v1/projects" {
		if method == "GET" {
			params := map[string]string{}
			for k, v := range r.URL.Query() {
				params[k] = v[0]
			}
			items, total, asOf, e := a.listAt(ctx, u, params)
			var le listError
			if errors.As(e, &le) {
				fail(w, 400, "INVALID_FILTER", le.Error())
				return nil
			}
			if e != nil {
				return e
			}
			respond(w, 200, map[string]any{"items": items, "total": total, "asOf": asOf})
			return nil
		}
		if method == "POST" {
			return a.createProject(w, r, u)
		}
	}
	prefix := "/api/v1/projects/"
	if !strings.HasPrefix(path, prefix) {
		fail(w, 404, "NOT_FOUND", "接口不存在")
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	id := parts[0]
	if !uuidPattern.MatchString(id) {
		fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
		return nil
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if len(parts) > 2 {
		fail(w, 404, "NOT_FOUND", "接口不存在")
		return nil
	}
	if method == "GET" {
		p, e := get(ctx, a.db, id, false)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e != nil || !canRead(u, p) {
			fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
			return nil
		}
		switch action {
		case "":
			if e = a.governanceHistory(ctx, &p); e != nil {
				return e
			}
			rows, e := a.db.Query(ctx, `SELECT a.type,a.occurred_at,a.summary,a.result,a.next_action,coalesce(to_char(a.next_followup_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),`+displayName("a.subject")+` FROM project_activities a WHERE a.project_id=$1 ORDER BY a.occurred_at DESC,a.created_at DESC`, id)
			if e != nil {
				return e
			}
			p.Activities = []Activity{}
			for rows.Next() {
				var x Activity
				if e = rows.Scan(&x.Type, &x.OccurredAt, &x.Summary, &x.Result, &x.NextAction, &x.NextFollowupAt, &x.ActorName); e != nil {
					rows.Close()
					return e
				}
				p.Activities = append(p.Activities, x)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			rows, e = a.db.Query(ctx, `SELECT a.action,a.revision,a.created_at,a.subject,a.details,`+displayName("a.subject")+` FROM audit_events a WHERE a.project_id=$1 ORDER BY a.created_at DESC,a.id`, id)
			if e != nil {
				return e
			}
			defer rows.Close()
			p.Audit = []Audit{}
			for rows.Next() {
				var x Audit
				var details []byte
				if e = rows.Scan(&x.Action, &x.Revision, &x.CreatedAt, &x.Subject, &details, &x.ActorName); e != nil {
					return e
				}
				if e = json.Unmarshal(details, &x.Details); e != nil {
					return e
				}
				p.Audit = append(p.Audit, x)
			}
			if e = rows.Err(); e != nil {
				return e
			}
			if e = a.projectHints(ctx, a.db, u, &p); e != nil {
				return e
			}
			respond(w, 200, p)
		case "duplicates":
			ds, e := a.duplicates(ctx, a.db, p.Input, p.ID)
			if e != nil {
				return e
			}
			respond(w, 200, map[string]any{"items": redact(ds, u)})
		default:
			fail(w, 404, "NOT_FOUND", "接口不存在")
		}
		return nil
	}
	if !(method == "PATCH" && action == "" || method == "POST" && contains([]string{"submit", "activities"}, action)) {
		fail(w, 405, "METHOD_NOT_ALLOWED", "操作不支持")
		return nil
	}
	required := 0
	if action == "submit" {
		required = 2
	}
	if action == "activities" {
		required = 4
	}
	tx, e := a.beginBusiness(ctx, u, required)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if action == "submit" {
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727102)`); e != nil {
			return e
		}
	}
	p, e := get(ctx, tx, id, true)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if e != nil || !canRead(u, p) {
		fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
		return nil
	}
	policyAction := action
	if policyAction == "" {
		policyAction = "edit"
	}
	if e = enforceProject(u, p, policyAction); e != nil {
		return e
	}
	var in Input
	var activity Activity
	var version int
	switch action {
	case "submit":
		var b struct {
			Version int `json:"version"`
		}
		if !decode(w, r, &b) {
			return nil
		}
		version = b.Version
	case "activities":
		if !decode(w, r, &activity) {
			return nil
		}
		version = activity.Version
	default:
		if !decode(w, r, &in) {
			return nil
		}
		version = in.Version
	}
	if version != p.Version {
		fail(w, 409, "VERSION_CONFLICT", "项目已更新，请重新加载后重试")
		return nil
	}
	event := "project.edit"
	if action == "activities" {
		if !contains([]string{"SUBMITTED", "UNDER_REVIEW", "APPROVED"}, p.IntakeStatus) {
			fail(w, 409, "INVALID_STATE", "当前状态不能记录跟进")
			return nil
		}
		if !contains(activityTypes, activity.Type) || activity.OccurredAt.IsZero() || strings.TrimSpace(activity.Summary) == "" || len(activity.Summary) > 6000 || len(activity.Result) > 6000 || len(activity.NextAction) > 3000 {
			fail(w, 400, "INVALID_INPUT", "请填写有效跟进类型、时间与摘要")
			return nil
		}
		var next *time.Time
		if activity.NextFollowupAt != "" {
			t, e := time.Parse(time.RFC3339, activity.NextFollowupAt)
			if e != nil {
				fail(w, 400, "INVALID_INPUT", "下次跟进时间不正确")
				return nil
			}
			next = &t
		}
		if _, e = tx.Exec(ctx, `INSERT INTO project_activities(project_id,subject,type,occurred_at,summary,result,next_action,next_followup_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, u.Subject, activity.Type, activity.OccurredAt, activity.Summary, activity.Result, activity.NextAction, next); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE intake_projects SET last_activity_at=greatest(last_activity_at,$2),next_followup_at=$3,next_action=$4,version=version+1,updated_at=now() WHERE id=$1`, id, activity.OccurredAt, next, activity.NextAction); e != nil {
			return e
		}
		event = "activity.create"
	} else {
		if !editable(p.IntakeStatus) {
			fail(w, 409, "INVALID_STATE", "当前状态不能编辑或重新提交")
			return nil
		}
		if action == "submit" {
			in = p.Input
			team, err := activeOwner(ctx, tx, p.OwnerSubject)
			if err != nil {
				return err
			}
			if team != p.TeamID {
				return problem(409, "OWNER_INACTIVE")
			}
			var confirmed bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM duplicate_cases WHERE project_id=$1 AND decision='CONFIRMED_DUPLICATE')`, id).Scan(&confirmed); err != nil {
				return err
			}
			if confirmed {
				return problem(409, "DUPLICATE_ALREADY_CONFIRMED")
			}
			if e = validateInput(&in, true); e != nil {
				fail(w, 400, "INVALID_INPUT", e.Error())
				return nil
			}
			if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fingerprint(in)); e != nil {
				return e
			}
			ds, e := a.duplicates(ctx, tx, in, id)
			if e != nil {
				return e
			}
			p.IntakeStatus = "SUBMITTED"
			if len(ds) > 0 {
				p.IntakeStatus = "UNDER_REVIEW"
			}
			if e = freezeSubmission(ctx, tx, p, u); e != nil {
				return e
			}
			for _, d := range ds {
				if _, e = tx.Exec(ctx, `INSERT INTO duplicate_cases(project_id,candidate_id,score,risk,matched_signals,submission_revision) VALUES($1,$2,$3,$4,$5,$6)`, id, d.CandidateID, d.Score, d.Risk, mustJSON(d.Signals), p.SubmissionRevision+1); e != nil {
					return e
				}
			}
			if _, e = tx.Exec(ctx, `UPDATE intake_projects SET submitted_by_subject=$2,submission_revision=submission_revision+1,
            protection_started_at=CASE WHEN protection_basis='FIRST_SUBMISSION' THEN coalesce(protection_started_at,now()) ELSE protection_started_at END WHERE id=$1`, id, u.Subject); e != nil {
				return e
			}
			event = "project.submit"
		} else {
			if e = validateInput(&in, false); e != nil {
				fail(w, 400, "INVALID_INPUT", e.Error())
				return nil
			}
		}
		in.Version = p.Version + 1
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE intake_projects SET project_name=$2,normalized_project_name=$3,sales_stage=$4,information_source=$5,confidence_level=$6,project_type=$7,estimated_amount=$8,expected_purchase_date=NULLIF($9,'')::date,document=$10,intake_status=$11,version=version+1,updated_at=now() WHERE id=$1`, id, in.ProjectName, normalize(in.ProjectName), in.SalesStage, in.InformationSource, in.ConfidenceLevel, in.ProjectType, in.EstimatedAmount, in.ExpectedPurchaseDate, b, p.IntakeStatus); e != nil {
			return e
		}
		if action != "submit" && in.SalesStage != p.SalesStage && !validStage(p.SalesStage, in.SalesStage, p.IntakeStatus, false) {
			fail(w, 409, "INVALID_STATE", "阶段必须正常向前或明确暂停/结束")
			return nil
		}
		if action != "submit" {
			if _, e = tx.Exec(ctx, `UPDATE intake_projects SET next_action=$2,next_followup_at=$3 WHERE id=$1`, id, in.NextAction, in.NextFollowupAt); e != nil {
				return e
			}
			updated, err := get(ctx, tx, id, false)
			if err != nil {
				return err
			}
			if e = participation(ctx, tx, updated, u, "EDIT"); e != nil {
				return e
			}
		}
		p.Input = in
		if e = relations(ctx, tx, p); e != nil {
			return e
		}
	}
	if action == "activities" || action == "submit" {
		if _, e = a.refreshProtection(ctx, tx, id, time.Now().UTC(), false); e != nil {
			return e
		}
	}
	p, e = get(ctx, tx, id, false)
	if e != nil {
		return e
	}
	if e = audit(ctx, tx, p, u, event); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if e = a.projectHints(ctx, a.db, u, &p); e != nil {
		return e
	}
	respond(w, 200, p)
	return nil
}

type TransferPreview struct {
	SourceIntakeID        string `json:"sourceIntakeId"`
	SourceRevision        int    `json:"sourceRevision"`
	PlannedEquipmentCount int    `json:"plannedEquipmentCount"`
	Connected             bool   `json:"connected"`
}
type TargetAdapter interface {
	Validate(context.Context, []byte) (TransferPreview, error)
	Commit(context.Context, []byte) (string, error)
}
type UnconnectedAdapter struct{}

func (UnconnectedAdapter) Validate(context.Context, []byte) (TransferPreview, error) {
	return TransferPreview{Connected: false}, errors.New("Crane adapter not connected")
}
func (UnconnectedAdapter) Commit(context.Context, []byte) (string, error) {
	return "", errors.New("Crane adapter not connected")
}
