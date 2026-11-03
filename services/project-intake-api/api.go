package main

import (
	"context"
	"encoding/json"
	"errors"
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
			if a.db.Ping(ctx) != nil {
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
			fail(w, 401, "UNAUTHENTICATED", "登录已失效，请重新登录")
			return
		}
		if r.Method != "GET" && !a.csrf(w, r, u) {
			return
		}
		if e = a.api(w, r, u); e != nil {
			fail(w, 503, "SERVICE_UNAVAILABLE", "服务暂时不可用，请保留输入并重试")
		}
	})
}
func (a *App) api(w http.ResponseWriter, r *http.Request, u User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	path, method := r.URL.Path, r.Method
	if handled, e := a.governance(w, r, u); handled {
		return e
	}
	if path == "/api/v1/me" && method == "GET" {
		respond(w, 200, u)
		return nil
	}
	if path == "/api/v1/reference-values" && method == "GET" {
		respond(w, 200, map[string]any{"salesStages": stages, "informationSources": sources, "confidenceLevels": confidence, "requirements": requirements, "activityTypes": activityTypes})
		return nil
	}
	if path == "/api/v1/duplicate-checks" && method == "POST" {
		if u.Role != "baobei_sales" {
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
	if path == "/api/v1/projects" {
		if method == "GET" {
			params := map[string]string{}
			for k, v := range r.URL.Query() {
				params[k] = v[0]
			}
			items, total, e := a.list(ctx, u, params)
			if e != nil {
				return e
			}
			respond(w, 200, map[string]any{"items": items, "total": total})
			return nil
		}
		if method == "POST" {
			if u.Role != "baobei_sales" {
				fail(w, 403, "FORBIDDEN", "仅业务员可创建报备")
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
			in.Version = 1
			b, e := json.Marshal(in)
			if e != nil {
				return e
			}
			tx, e := a.db.Begin(ctx)
			if e != nil {
				return e
			}
			defer tx.Rollback(ctx)
			var id string
			e = tx.QueryRow(ctx, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,team_id,sales_stage,information_source,confidence_level,project_type,estimated_amount,expected_purchase_date,document) VALUES($1,$2,$3,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9,NULLIF($10,'')::date,$11) RETURNING id::text`, in.ProjectName, normalize(in.ProjectName), u.Subject, u.TeamID, in.SalesStage, in.InformationSource, in.ConfidenceLevel, in.ProjectType, in.EstimatedAmount, in.ExpectedPurchaseDate, b).Scan(&id)
			if e != nil {
				return e
			}
			p, e := get(ctx, tx, id, false)
			if e != nil {
				return e
			}
			if e = relations(ctx, tx, p); e != nil {
				return e
			}
			if e = audit(ctx, tx, p, u, "project.create"); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `INSERT INTO ownership_history(project_id,to_subject,reason) VALUES($1,$2,'首次创建，由会话确定')`, id, u.Subject); e != nil {
				return e
			}
			if e = tx.Commit(ctx); e != nil {
				return e
			}
			respond(w, 200, p)
			return nil
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
		if e != nil || !canRead(u, p) {
			fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
			return nil
		}
		switch action {
		case "":
			if e = a.governanceHistory(ctx, &p); e != nil {
				return e
			}
			rows, e := a.db.Query(ctx, `SELECT type,occurred_at,summary,result,next_action,coalesce(to_char(next_followup_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'') FROM project_activities WHERE project_id=$1 ORDER BY occurred_at DESC,created_at DESC`, id)
			if e != nil {
				return e
			}
			p.Activities = []Activity{}
			for rows.Next() {
				var x Activity
				if e = rows.Scan(&x.Type, &x.OccurredAt, &x.Summary, &x.Result, &x.NextAction, &x.NextFollowupAt); e != nil {
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
			rows, e = a.db.Query(ctx, `SELECT action,revision,created_at,subject,details FROM audit_events WHERE project_id=$1 ORDER BY created_at DESC`, id)
			if e != nil {
				return e
			}
			defer rows.Close()
			p.Audit = []Audit{}
			for rows.Next() {
				var x Audit
				var details []byte
				if e = rows.Scan(&x.Action, &x.Revision, &x.CreatedAt, &x.Subject, &details); e != nil {
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
	tx, e := a.db.Begin(ctx)
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
	if e != nil || !canRead(u, p) {
		fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
		return nil
	}
	if !canWrite(u, p) {
		fail(w, 403, "FORBIDDEN", "仅负责人可操作此项目")
		return nil
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
		if _, e = tx.Exec(ctx, `UPDATE intake_projects SET last_activity_at=greatest(last_activity_at,$2),next_followup_at=$3,version=version+1,updated_at=now() WHERE id=$1`, id, activity.OccurredAt, next); e != nil {
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
			if e = validateInput(&in, true); e != nil {
				fail(w, 400, "INVALID_INPUT", e.Error())
				return nil
			}
			if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fingerprint(in)); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `DELETE FROM duplicate_cases WHERE project_id=$1 AND decision='PENDING'`, id); e != nil {
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
			for _, d := range ds {
				if _, e = tx.Exec(ctx, `INSERT INTO duplicate_cases(project_id,candidate_id,score,risk,matched_signals) VALUES($1,$2,$3,$4,$5)`, id, d.CandidateID, d.Score, d.Risk, mustJSON(d.Signals)); e != nil {
					return e
				}
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
