package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// V1.1 R6 (B3) industrial technical requirements. The filed document,
// planned_equipment, project_requirements and frozen submissions are never
// rewritten; the technical brief is a separate, versioned record with an
// immutable revision per save, and clarification questions keep an immutable
// event trail. Rights follow the project:
//
//	read brief/questions  whoever may read the project
//	edit brief            the project's writer (owner; administrator)
//	ask a question        owner, the team's supervisor, directors, administrators
//	answer                the owner (administrator)
//	close / reopen        the asker or the owner
//
// No new role and no cross-team sharing (B6 is not approved).

type TechParam struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Unit  string `json:"unit"`
}
type TechEquipment struct {
	EquipmentType string   `json:"equipmentType"`
	Quantity      *int     `json:"quantity"`
	CapacityTons  *float64 `json:"capacityTons"`
	SpanM         *float64 `json:"spanM"`
	LiftHeightM   *float64 `json:"liftHeightM"`
	Notes         string   `json:"notes"`
}
type TechnicalBrief struct {
	ProjectID       string          `json:"projectId"`
	Version         int             `json:"version"`
	Status          string          `json:"status"`
	ProjectNature   string          `json:"projectNature"`
	Scenario        string          `json:"scenario"`
	Workshop        string          `json:"workshop"`
	ProductionLine  string          `json:"productionLine"`
	SiteConstraints string          `json:"siteConstraints"`
	Parameters      []TechParam     `json:"parameters"`
	Equipment       []TechEquipment `json:"equipment"`
	UpdatedByName   string          `json:"updatedByName"`
	UpdatedAt       *time.Time      `json:"updatedAt"`
	// Filed: the equipment and requirements as filed (read-only reference).
	Filed     map[string]any        `json:"filed"`
	Questions []TechnicalQuestion   `json:"questions"`
	Revisions []TechnicalRevision   `json:"revisions"`
	Actions   map[string]ActionHint `json:"actions"`
}
type TechnicalQuestion struct {
	ID             string    `json:"id"`
	Question       string    `json:"question"`
	Status         string    `json:"status"`
	Answer         string    `json:"answer"`
	AskedByName    string    `json:"askedByName"`
	AnsweredByName string    `json:"answeredByName"`
	BriefVersion   int       `json:"briefVersion"`
	Version        int       `json:"version"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	CanAnswer      bool      `json:"canAnswer"`
	CanClose       bool      `json:"canClose"`
	asker          string
}
type TechnicalRevision struct {
	Version   int       `json:"version"`
	ActorName string    `json:"actorName"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

var (
	techStatuses = []string{"NOT_STARTED", "COLLECTING", "CLARIFIED"}
	techNatures  = []string{"", "NEW_BUILD", "RETROFIT", "EXPANSION", "REPLACEMENT"}
	techConflict = staffProblem{409, "VERSION_CONFLICT", "技术需求已被其他人更新，请刷新后对比再保存"}
)

func techWriter(u User, p Project) bool { return canWrite(u, p) }
func techAsker(u User, p Project) bool {
	return canRead(u, p) && (u.Role == adminRole || u.Role == directorRole || u.Role == managerRole || p.OwnerSubject == u.Subject)
}
func techOpen(p Project) bool {
	return p.DiscardedAt == nil && !contains([]string{"CANCELLED", "CONVERTED"}, p.IntakeStatus)
}

func (a *App) technicalBrief(ctx context.Context, q querier, u User, p Project) (TechnicalBrief, error) {
	b := TechnicalBrief{ProjectID: p.ID, Status: "NOT_STARTED", Parameters: []TechParam{}, Equipment: []TechEquipment{}}
	var params, equipment []byte
	err := q.QueryRow(ctx, `SELECT version,status,project_nature,scenario,workshop,production_line,site_constraints,parameters,equipment,`+displayName("updated_by_subject")+`,updated_at
 FROM project_technical_briefs WHERE project_id=$1`, p.ID).Scan(&b.Version, &b.Status, &b.ProjectNature, &b.Scenario, &b.Workshop, &b.ProductionLine, &b.SiteConstraints, &params, &equipment, &b.UpdatedByName, &b.UpdatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return b, err
	}
	if err == nil {
		_ = json.Unmarshal(params, &b.Parameters)
		_ = json.Unmarshal(equipment, &b.Equipment)
	}
	b.Filed = map[string]any{"plannedEquipment": p.PlannedEquipment, "requirements": p.Requirements, "requirementNotes": p.RequirementNotes, "projectType": p.ProjectType, "workshop": p.Location.Workshop}
	rows, err := q.Query(ctx, `SELECT q.id::text,q.question,q.status,q.answer,`+displayName("q.asked_by_subject")+`,`+displayName("q.answered_by_subject")+`,q.brief_version,q.version,q.created_at,q.updated_at,q.asked_by_subject
 FROM project_technical_questions q WHERE q.project_id=$1 ORDER BY q.created_at,q.id`, p.ID)
	if err != nil {
		return b, err
	}
	b.Questions = []TechnicalQuestion{}
	for rows.Next() {
		var t TechnicalQuestion
		if err = rows.Scan(&t.ID, &t.Question, &t.Status, &t.Answer, &t.AskedByName, &t.AnsweredByName, &t.BriefVersion, &t.Version, &t.CreatedAt, &t.UpdatedAt, &t.asker); err != nil {
			rows.Close()
			return b, err
		}
		t.CanAnswer = techOpen(p) && techWriter(u, p) && t.Status != "CLOSED"
		t.CanClose = techOpen(p) && (t.asker == u.Subject || techWriter(u, p))
		b.Questions = append(b.Questions, t)
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT version,`+displayName("actor_subject")+`,reason,at FROM project_technical_brief_revisions WHERE project_id=$1 ORDER BY version DESC LIMIT 30`, p.ID)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	b.Revisions = []TechnicalRevision{}
	for rows.Next() {
		var r TechnicalRevision
		if err = rows.Scan(&r.Version, &r.ActorName, &r.Reason, &r.At); err != nil {
			return b, err
		}
		b.Revisions = append(b.Revisions, r)
	}
	b.Actions = map[string]ActionHint{"technical.edit": hint("BUSINESS_PERMISSION_DENIED"), "technical.ask": hint("BUSINESS_PERMISSION_DENIED")}
	if !techOpen(p) {
		b.Actions["technical.edit"], b.Actions["technical.ask"] = hint("INVALID_STATE"), hint("INVALID_STATE")
	} else {
		if techWriter(u, p) {
			b.Actions["technical.edit"] = hint("")
		}
		if techAsker(u, p) {
			b.Actions["technical.ask"] = hint("")
		}
	}
	return b, rows.Err()
}

type briefInput struct {
	Version         int             `json:"version"`
	Status          string          `json:"status"`
	ProjectNature   string          `json:"projectNature"`
	Scenario        string          `json:"scenario"`
	Workshop        string          `json:"workshop"`
	ProductionLine  string          `json:"productionLine"`
	SiteConstraints string          `json:"siteConstraints"`
	Parameters      []TechParam     `json:"parameters"`
	Equipment       []TechEquipment `json:"equipment"`
	Reason          string          `json:"reason"`
}

func (in *briefInput) valid() error {
	if !contains(techStatuses, in.Status) || !contains(techNatures, in.ProjectNature) {
		return staffProblem{400, "INVALID_INPUT", "技术状态或项目性质不正确"}
	}
	for _, f := range []struct {
		v   *string
		max int
	}{{&in.Scenario, 2000}, {&in.Workshop, 200}, {&in.ProductionLine, 200}, {&in.SiteConstraints, 4000}, {&in.Reason, 600}} {
		*f.v = strings.TrimSpace(*f.v)
		if !textOK(*f.v, f.max) {
			return staffProblem{400, "INVALID_INPUT", "技术需求文本过长或包含不允许的字符"}
		}
	}
	if in.Parameters == nil {
		in.Parameters = []TechParam{}
	}
	if in.Equipment == nil {
		in.Equipment = []TechEquipment{}
	}
	if len(in.Parameters) > 50 || len(in.Equipment) > 30 {
		return staffProblem{400, "INVALID_INPUT", "技术参数最多 50 项，设备最多 30 项"}
	}
	for i := range in.Parameters {
		p := &in.Parameters[i]
		p.Key, p.Value, p.Unit = strings.TrimSpace(p.Key), strings.TrimSpace(p.Value), strings.TrimSpace(p.Unit)
		if p.Key == "" || !textOK(p.Key, 60) || !textOK(p.Value, 200) || !textOK(p.Unit, 20) {
			return fieldProblem{staffProblem{400, "TECH_PARAM_INVALID", "每个技术参数需要名称（60 字内），数值 200 字内"}, "parameters"}
		}
	}
	for i := range in.Equipment {
		e := &in.Equipment[i]
		e.EquipmentType, e.Notes = strings.TrimSpace(e.EquipmentType), strings.TrimSpace(e.Notes)
		if e.EquipmentType == "" || !textOK(e.EquipmentType, 60) || !textOK(e.Notes, 500) ||
			e.Quantity != nil && (*e.Quantity < 0 || *e.Quantity > 1000) || badNumber(e.CapacityTons, 2000) || badNumber(e.SpanM, 200) || badNumber(e.LiftHeightM, 200) {
			return fieldProblem{staffProblem{400, "TECH_EQUIPMENT_INVALID", "请检查设备类型、数量和参数范围"}, "equipment"}
		}
	}
	return nil
}

func (a *App) technicalHandler(w http.ResponseWriter, r *http.Request, u User, id, sub string) error {
	ctx := r.Context()
	p, err := get(ctx, a.db, id, false)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err != nil || !canRead(u, p) {
		fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
		return nil
	}
	if r.Method == "GET" && sub == "" {
		b, err := a.technicalBrief(ctx, a.db, u, p)
		if err != nil {
			return err
		}
		respond(w, 200, b)
		return nil
	}
	tx, err := a.beginBusiness(ctx, u, 0)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize with other writes of this project (lock order: identity, project).
	if p, err = get(ctx, tx, id, true); err != nil {
		return err
	}
	// Re-check access on the locked row (ownership or team may have changed).
	if !canRead(u, p) {
		fail(w, 404, "NOT_FOUND", "项目不存在或无权访问")
		return nil
	}
	if !techOpen(p) {
		return problem(409, "INVALID_STATE")
	}
	switch {
	case r.Method == "PUT" && sub == "":
		var in briefInput
		if !decode(w, r, &in) {
			return nil
		}
		if !techWriter(u, p) {
			return problem(403, "BUSINESS_PERMISSION_DENIED")
		}
		if err = in.valid(); err != nil {
			return err
		}
		var current int
		if err = tx.QueryRow(ctx, `SELECT coalesce((SELECT version FROM project_technical_briefs WHERE project_id=$1 FOR UPDATE),0)`, p.ID).Scan(&current); err != nil {
			return err
		}
		if in.Version != current {
			return techConflict
		}
		next := current + 1
		if _, err = tx.Exec(ctx, `INSERT INTO project_technical_briefs(project_id,version,status,project_nature,scenario,workshop,production_line,site_constraints,parameters,equipment,updated_by_subject)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
 ON CONFLICT(project_id) DO UPDATE SET version=excluded.version,status=excluded.status,project_nature=excluded.project_nature,scenario=excluded.scenario,workshop=excluded.workshop,
 production_line=excluded.production_line,site_constraints=excluded.site_constraints,parameters=excluded.parameters,equipment=excluded.equipment,updated_by_subject=excluded.updated_by_subject,updated_at=now()`,
			p.ID, next, in.Status, in.ProjectNature, in.Scenario, in.Workshop, in.ProductionLine, in.SiteConstraints, mustJSON(in.Parameters), mustJSON(in.Equipment), u.Subject); err != nil {
			return err
		}
		snapshot := in
		snapshot.Reason, snapshot.Version = "", next
		if _, err = tx.Exec(ctx, `INSERT INTO project_technical_brief_revisions(project_id,version,snapshot,actor_subject,reason) VALUES($1,$2,$3,$4,$5)`, p.ID, next, mustJSON(snapshot), u.Subject, in.Reason); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(project_id,subject,action,revision,details) VALUES($1,$2,'technical.brief',$3,$4)`, p.ID, u.Subject, p.Version, mustJSON(map[string]any{"briefVersion": next, "status": in.Status, "reason": in.Reason})); err != nil {
			return err
		}
	case r.Method == "POST" && sub == "questions":
		var in struct {
			Question string `json:"question"`
		}
		if !decode(w, r, &in) {
			return nil
		}
		if !techAsker(u, p) {
			return problem(403, "BUSINESS_PERMISSION_DENIED")
		}
		in.Question = strings.TrimSpace(in.Question)
		if in.Question == "" || !textOK(in.Question, 2000) {
			return fieldProblem{staffProblem{400, "TECH_QUESTION_INVALID", "请填写待澄清问题（2000 字内）"}, "question"}
		}
		// AC-23: the project row is locked; the same open question by the same
		// asker (a retry after a timeout or a double click) is not recorded twice.
		var dup bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_technical_questions WHERE project_id=$1 AND asked_by_subject=$2 AND status='OPEN' AND question=$3)`, p.ID, u.Subject, in.Question).Scan(&dup); err != nil {
			return err
		}
		if dup {
			break
		}
		var qid string
		if err = tx.QueryRow(ctx, `INSERT INTO project_technical_questions(project_id,question,status,asked_by_subject,brief_version)
 VALUES($1,$2,'OPEN',$3,coalesce((SELECT version FROM project_technical_briefs WHERE project_id=$1),0)) RETURNING id::text`, p.ID, in.Question, u.Subject).Scan(&qid); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO project_technical_question_events(question_id,project_id,actor_subject,action,text) VALUES($1,$2,$3,'question.ask',$4)`, qid, p.ID, u.Subject, in.Question); err != nil {
			return err
		}
	case r.Method == "POST" && strings.HasPrefix(sub, "questions/"):
		parts := strings.Split(strings.TrimPrefix(sub, "questions/"), "/")
		if len(parts) != 2 || !uuidPattern.MatchString(parts[0]) || !contains([]string{"answer", "close", "reopen"}, parts[1]) {
			fail(w, 404, "NOT_FOUND", "接口不存在")
			return nil
		}
		var in struct {
			Answer  string `json:"answer"`
			Version int    `json:"version"`
		}
		if !decode(w, r, &in) {
			return nil
		}
		var status, asker string
		var version int
		err = tx.QueryRow(ctx, `SELECT status,asked_by_subject,version FROM project_technical_questions WHERE id=$1 AND project_id=$2 FOR UPDATE`, parts[0], p.ID).Scan(&status, &asker, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 404, "NOT_FOUND", "澄清问题不存在")
			return nil
		}
		if err != nil {
			return err
		}
		if in.Version != version {
			return techConflict
		}
		action, next, text := "question."+parts[1], status, ""
		switch parts[1] {
		case "answer":
			if !techWriter(u, p) {
				return problem(403, "BUSINESS_PERMISSION_DENIED")
			}
			in.Answer = strings.TrimSpace(in.Answer)
			if status == "CLOSED" || in.Answer == "" || !textOK(in.Answer, 4000) {
				return fieldProblem{staffProblem{400, "TECH_ANSWER_INVALID", "请填写答复（4000 字内）；已关闭的问题需先重新打开"}, "answer"}
			}
			next, text = "ANSWERED", in.Answer
			_, err = tx.Exec(ctx, `UPDATE project_technical_questions SET answer=$2,answered_by_subject=$3,status='ANSWERED',version=version+1,updated_at=now() WHERE id=$1`, parts[0], in.Answer, u.Subject)
		case "close", "reopen":
			if asker != u.Subject && !techWriter(u, p) {
				return problem(403, "BUSINESS_PERMISSION_DENIED")
			}
			if (parts[1] == "close") == (status == "CLOSED") {
				return problem(409, "INVALID_STATE")
			}
			next = "CLOSED"
			if parts[1] == "reopen" {
				next = "OPEN"
			}
			_, err = tx.Exec(ctx, `UPDATE project_technical_questions SET status=$2,version=version+1,updated_at=now() WHERE id=$1`, parts[0], next)
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO project_technical_question_events(question_id,project_id,actor_subject,action,text) VALUES($1,$2,$3,$4,$5)`, parts[0], p.ID, u.Subject, action, text); err != nil {
			return err
		}
	default:
		fail(w, 405, "METHOD_NOT_ALLOWED", "操作不支持")
		return nil
	}
	b, err := a.technicalBrief(ctx, tx, u, p)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	respond(w, 200, b)
	return nil
}
