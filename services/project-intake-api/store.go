package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
	"time"
)

type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

const columns = `p.protected_until,p.next_review_at,p.id::text,p.registration_no,p.owner_subject,p.created_by_subject,coalesce(p.team_id::text,''),p.intake_status,p.protection_status,p.version,p.created_at,p.updated_at,p.last_activity_at,p.next_followup_at,p.document,coalesce((SELECT next_action FROM project_activities WHERE project_id=p.id ORDER BY created_at DESC LIMIT 1),'')`

func scan(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var b []byte
	e := row.Scan(&p.ProtectedUntil, &p.NextReviewAt, &p.ID, &p.RegistrationNo, &p.OwnerSubject, &p.CreatedBySubject, &p.TeamID, &p.IntakeStatus, &p.ProtectionStatus, &p.Version, &p.CreatedAt, &p.UpdatedAt, &p.LastActivityAt, &p.NextFollowupAt, &b, &p.NextAction)
	if e != nil {
		return p, e
	}
	v := p.Version
	e = json.Unmarshal(b, &p.Input)
	p.Version = v
	return p, e
}
func get(ctx context.Context, q querier, id string, lock bool) (Project, error) {
	sql := `SELECT ` + columns + ` FROM intake_projects p WHERE p.id=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scan(q.QueryRow(ctx, sql, id))
}
func relations(ctx context.Context, tx pgx.Tx, p Project) error {
	l := p.Location
	if _, e := tx.Exec(ctx, `INSERT INTO project_locations VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(project_id) DO UPDATE SET province=$2,city=$3,district=$4,industrial_park=$5,factory_name=$6,workshop=$7,address_detail=$8`, p.ID, l.Province, l.City, l.District, l.IndustrialPark, l.FactoryName, l.Workshop, l.AddressDetail); e != nil {
		return e
	}
	for _, table := range []string{"project_parties", "planned_equipment", "project_requirements"} {
		if _, e := tx.Exec(ctx, `DELETE FROM `+table+` WHERE project_id=$1`, p.ID); e != nil {
			return e
		}
	}
	for _, party := range append([]Party{{Role: "END_CUSTOMER", Name: p.Customer}}, p.Parties...) {
		if party.Name == "" {
			continue
		}
		var org string
		if e := tx.QueryRow(ctx, `INSERT INTO organizations(name,normalized_name) VALUES($1,$2) ON CONFLICT(normalized_name) DO UPDATE SET normalized_name=excluded.normalized_name RETURNING id::text`, party.Name, normalize(party.Name)).Scan(&org); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `INSERT INTO project_parties(project_id,role,organization_id) VALUES($1,$2,$3)`, p.ID, party.Role, org); e != nil {
			return e
		}
	}
	for _, eq := range p.PlannedEquipment {
		b, e := json.Marshal(eq)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO planned_equipment(project_id,data) VALUES($1,$2)`, p.ID, b); e != nil {
			return e
		}
	}
	for _, r := range p.Requirements {
		if _, e := tx.Exec(ctx, `INSERT INTO project_requirements VALUES($1,$2,$3)`, p.ID, r, p.RequirementNotes); e != nil {
			return e
		}
	}
	return nil
}
func audit(ctx context.Context, tx pgx.Tx, p Project, u User, action string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(project_id,subject,action,revision) VALUES($1,$2,$3,$4)`, p.ID, u.Subject, action, p.Version)
	return e
}
func (a *App) duplicates(ctx context.Context, q querier, p Input, exclude string) ([]Duplicate, error) {
	var t struct {
		Warn int `json:"warn"`
		High int `json:"high"`
	}
	var b []byte
	if e := q.QueryRow(ctx, `SELECT value FROM reference_values WHERE key='duplicate_thresholds'`).Scan(&b); e != nil {
		return nil, e
	}
	if e := json.Unmarshal(b, &t); e != nil {
		return nil, e
	}
	if t.Warn < 1 || t.High < t.Warn || t.High > 100 {
		return nil, errors.New("invalid thresholds")
	}
	rows, e := q.Query(ctx, `SELECT p.id::text,p.document,p.created_at,similarity(p.normalized_project_name,$1),EXISTS(SELECT 1 FROM project_parties pp JOIN organizations o ON o.id=pp.organization_id LEFT JOIN organization_aliases al ON al.organization_id=o.id WHERE pp.project_id=p.id AND pp.role='END_CUSTOMER' AND (o.normalized_name=$2 OR al.normalized_alias=$2)) FROM intake_projects p WHERE p.intake_status IN ('SUBMITTED','UNDER_REVIEW','APPROVED') AND ($3='' OR p.id::text<>$3)`, normalize(p.ProjectName), normalize(p.Customer), exclude)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Duplicate{}
	for rows.Next() {
		var q Input
		var id string
		var doc []byte
		var at time.Time
		var sim float64
		var same bool
		if e = rows.Scan(&id, &doc, &at, &sim, &same); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(doc, &q); e != nil {
			return nil, e
		}
		n := score(p, q, sim, same)
		if n < t.Warn {
			continue
		}
		risk := "WARN"
		if n >= t.High {
			risk = "HIGH"
		}
		out = append(out, Duplicate{Signals: duplicateSignals(p, q, sim, same), CandidateID: id, Score: n, Risk: risk, Region: q.Location.Province + " / " + q.Location.City, CreatedAt: at, SalesStage: q.SalesStage, Message: "疑似已有报备，需要主管确认", ProjectName: q.ProjectName})
	}
	return out, rows.Err()
}
func redact(ds []Duplicate, u User) []Duplicate {
	out := make([]Duplicate, len(ds))
	copy(out, ds)
	for i := range out {
		if u.Role != "baobei_director" {
			out[i].CandidateID = ""
			out[i].ProjectName = ""
		}
	}
	return out
}
func (a *App) list(ctx context.Context, u User, params map[string]string) ([]Project, int, error) {
	args := []any{u.Subject, u.Role, u.TeamID}
	where := ` WHERE (p.owner_subject=$1 OR $2='baobei_director' OR ($2='baobei_manager' AND p.team_id::text=$3 AND $3<>''))`
	add := func(sql string, v any) {
		args = append(args, v)
		where += strings.ReplaceAll(sql, "?", "$"+strconv.Itoa(len(args)))
	}
	if v := params["q"]; v != "" {
		add(` AND (p.project_name ILIKE '%'||?||'%' OR p.registration_no ILIKE '%'||?||'%' OR p.document->>'customer' ILIKE '%'||?||'%')`, v)
	}
	for k, col := range map[string]string{"status": "p.intake_status", "stage": "p.sales_stage", "province": "p.document->'location'->>'province'"} {
		if v := params[k]; v != "" {
			add(` AND `+col+`=?`, v)
		}
	}
	if v := params["updatedAfter"]; v != "" {
		if _, e := time.Parse(time.RFC3339, v); e != nil {
			return nil, 0, e
		}
		add(` AND p.updated_at>=?::timestamptz`, v)
	}
	if params["due"] == "true" {
		where += ` AND p.next_followup_at<=now()+interval '7 days'`
	}
	if params["review"] == "pending" {
		where += ` AND p.intake_status IN ('SUBMITTED','UNDER_REVIEW')`
	}
	if params["review"] == "protection" {
		where += ` AND p.protection_status IN ('WARNING','EXPIRING','EXPIRED') AND p.intake_status IN ('SUBMITTED','UNDER_REVIEW','APPROVED')`
	}
	var total int
	if e := a.db.QueryRow(ctx, `SELECT count(*) FROM intake_projects p`+where, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	sort := `p.updated_at DESC`
	if params["sort"] == "name" {
		sort = `p.project_name ASC`
	}
	page, _ := strconv.Atoi(params["page"])
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(params["pageSize"])
	if size < 1 || size > 100 {
		size = 20
	}
	args = append(args, size, (page-1)*size)
	rows, e := a.db.Query(ctx, `SELECT `+columns+` FROM intake_projects p`+where+` ORDER BY `+sort+`,p.id LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, e := scan(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}
