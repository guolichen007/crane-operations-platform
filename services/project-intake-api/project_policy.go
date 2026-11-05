package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
)

// ProjectPolicy is shared by SQL scopes, object reads, mutations and action hints.
type ProjectPolicy struct{ User User }
type ActionHint struct {
	Allowed    bool    `json:"allowed"`
	ReasonCode *string `json:"reasonCode"`
}

func hint(code string) ActionHint {
	if code == "" {
		return ActionHint{Allowed: true}
	}
	return ActionHint{ReasonCode: &code}
}
func problem(status int, code string) error {
	return staffProblem{status, code, "操作条件不满足，请刷新记录或联系管理员"}
}
func (s ProjectPolicy) Read(p Project) bool {
	u := s.User
	switch u.Role {
	case adminRole:
		return true
	case salesRole:
		return p.OwnerSubject != "" && p.OwnerSubject == u.Subject
	case managerRole:
		return p.OwnerSubject != "" && u.TeamID != "" && p.TeamID == u.TeamID
	case directorRole:
		return p.OwnerSubject != ""
	}
	return false
}

// The first three parameters are always subject, role and team, including counts.
func projectScope(alias string) string {
	return `($2='baobei_admin' OR (` + alias + `.owner_subject IS NOT NULL AND (
 ($2='baobei_sales' AND ` + alias + `.owner_subject=$1) OR $2='baobei_director' OR
 ($2='baobei_manager' AND $3<>'' AND ` + alias + `.team_id::text=$3))))`
}
func projectScopeAt(alias, subject, role, team string) string {
	return strings.NewReplacer("$1", subject, "$2", role, "$3", team).Replace(projectScope(alias))
}
func (s ProjectPolicy) Create() bool {
	return s.User.Role == adminRole || s.User.Role == salesRole && contains(s.User.Permissions, "project.create")
}
func (s ProjectPolicy) Manage() bool { return manager(s.User) || s.User.Role == adminRole }
func (s ProjectPolicy) Action(p Project, action string) string {
	u := s.User
	if !s.Read(p) {
		return "NOT_FOUND"
	}
	if p.OwnerSubject == "" && p.IntakeStatus != "DRAFT" {
		return "INVALID_STATE"
	}
	own := u.Role == salesRole && p.OwnerSubject == u.Subject
	writer := u.Role == adminRole || own
	switch action {
	case "edit":
		if !writer {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if !editable(p.IntakeStatus) {
			return "INVALID_STATE"
		}
	case "submit":
		if !writer || own && !contains(u.Permissions, "project.submit") {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if !editable(p.IntakeStatus) {
			return "INVALID_STATE"
		}
		if p.OwnerSubject == "" {
			return "OWNER_REQUIRED"
		}
	case "activities":
		if !writer || own && !contains(u.Permissions, "activity.create") {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if !contains([]string{"SUBMITTED", "UNDER_REVIEW", "APPROVED"}, p.IntakeStatus) {
			return "INVALID_STATE"
		}
	case "stage-change":
		if !writer {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if !editable(p.IntakeStatus) && !contains([]string{"SUBMITTED", "UNDER_REVIEW", "APPROVED"}, p.IntakeStatus) {
			return "INVALID_STATE"
		}
	case "ownership-transfer":
		if !s.Manage() {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if !editable(p.IntakeStatus) && !contains([]string{"SUBMITTED", "UNDER_REVIEW", "APPROVED"}, p.IntakeStatus) {
			return "INVALID_STATE"
		}
	case "approve", "return", "reject", "duplicate-decisions":
		if !s.Manage() {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if !contains([]string{"SUBMITTED", "UNDER_REVIEW"}, p.IntakeStatus) {
			return "INVALID_STATE"
		}
		if p.EvidenceState != "COMPLETE" || p.SubmissionRevision < 1 {
			return "REVIEW_EVIDENCE_INCOMPLETE"
		}
		if u.Subject == p.CreatedBySubject || u.Subject == p.OwnerSubject || u.Subject == p.SubmittedBySubject {
			return "REVIEW_CONFLICT"
		}
	default:
		return "BUSINESS_PERMISSION_DENIED"
	}
	return ""
}
func enforceProject(u User, p Project, action string) error {
	code := (ProjectPolicy{u}).Action(p, action)
	if code == "" {
		return nil
	}
	status := 403
	if code == "NOT_FOUND" {
		status = 404
	}
	if code == "INVALID_STATE" || code == "OWNER_REQUIRED" || code == "REVIEW_EVIDENCE_INCOMPLETE" {
		status = 409
	}
	return problem(status, code)
}
func reviewIndependence(ctx context.Context, q querier, u User, p Project) error {
	// Candidate projects need not themselves still be pending, but their evidence must be complete.
	if !(ProjectPolicy{u}).Read(p) {
		return problem(403, "REVIEW_RELATED_PROJECT_RESTRICTED")
	}
	if p.EvidenceState != "COMPLETE" || p.SubmissionRevision < 1 {
		return problem(409, "REVIEW_EVIDENCE_INCOMPLETE")
	}
	var conflict bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_submissions s WHERE s.project_id=$1 AND s.revision=$2
 AND (s.actor_subject=$3 OR s.creator_subject=$3 OR s.owner_subject=$3 OR s.participants ? $3
 OR EXISTS(SELECT 1 FROM project_participations a WHERE a.project_id=s.project_id AND a.source_version>=s.source_version
 AND (a.actor_subject=$3 OR a.owner_subject=$3))))`, p.ID, p.SubmissionRevision, u.Subject).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict || u.Subject == p.CreatedBySubject || u.Subject == p.OwnerSubject {
		return problem(403, "REVIEW_CONFLICT")
	}
	var complete bool
	err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_submissions WHERE project_id=$1 AND revision=$2 AND evidence_state='COMPLETE')`, p.ID, p.SubmissionRevision).Scan(&complete)
	if err != nil {
		return err
	}
	if !complete {
		return problem(409, "REVIEW_EVIDENCE_INCOMPLETE")
	}
	return nil
}
func activeOwner(ctx context.Context, q querier, subject string) (string, error) {
	if subject == "" {
		return "", problem(409, "OWNER_REQUIRED")
	}
	var team string
	err := q.QueryRow(ctx, `SELECT u.team_id::text FROM app_users u WHERE u.team_id IS NOT NULL AND u.subject=$1 AND u.active AND u.role='baobei_sales'`, subject).Scan(&team)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", problem(409, "OWNER_INACTIVE")
	}
	return team, err
}
func participation(ctx context.Context, tx pgx.Tx, p Project, u User, kind string) error {
	_, err := tx.Exec(ctx, `INSERT INTO project_participations(project_id,actor_subject,kind,source_version,owner_subject,team_id,document_hash)
 VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,'')::uuid,$7)`, p.ID, u.Subject, kind, p.Version, p.OwnerSubject, p.TeamID, p.DocumentHash)
	return err
}
func freezeSubmission(ctx context.Context, tx pgx.Tx, p Project, u User) error {
	_, err := tx.Exec(ctx, `INSERT INTO project_submissions(project_id,revision,source_version,actor_subject,creator_subject,owner_subject,team_id,document,document_hash,participants,evidence_state)
 SELECT $1,$2,$3,$4,$5,$6,$7::uuid,p.document,p.document_hash,coalesce((SELECT jsonb_agg(DISTINCT actor) FROM (
 SELECT actor_subject actor FROM project_participations WHERE project_id=$1 AND source_version>coalesce((SELECT max(revision) FROM audit_events WHERE project_id=$1 AND action='review.return'),0)
 UNION SELECT owner_subject FROM project_participations WHERE project_id=$1 AND owner_subject IS NOT NULL AND source_version>coalesce((SELECT max(revision) FROM audit_events WHERE project_id=$1 AND action='review.return'),0)
 ) a),'[]'::jsonb),$8 FROM intake_projects p WHERE id=$1`, p.ID, p.SubmissionRevision+1, p.Version, u.Subject, p.CreatedBySubject, p.OwnerSubject, p.TeamID, p.EvidenceState)
	return err
}

func (a *App) projectHints(ctx context.Context, q querier, u User, p *Project) error {
	p.AssignmentStatus = "ASSIGNED"
	if p.OwnerSubject == "" {
		p.AssignmentStatus = "UNASSIGNED_DRAFT"
	}
	p.Actions = map[string]ActionHint{}
	for _, action := range []string{"edit", "submit", "activities", "stage-change", "ownership-transfer", "approve", "return", "reject", "duplicate-decisions"} {
		code := (ProjectPolicy{u}).Action(*p, action)
		if code == "" && action == "submit" {
			team, err := activeOwner(ctx, q, p.OwnerSubject)
			if err != nil {
				var e staffProblem
				if !errors.As(err, &e) {
					return err
				}
				code = e.code
			} else if team != p.TeamID {
				code = "OWNER_INACTIVE"
			}
		}
		if code == "" && contains([]string{"approve", "return", "reject", "duplicate-decisions"}, action) {
			if err := reviewIndependence(ctx, q, u, *p); err != nil {
				var e staffProblem
				if !errors.As(err, &e) {
					return err
				}
				code = e.code
			}
		}
		if code == "" && action == "approve" {
			var pending int
			if err := q.QueryRow(ctx, `SELECT count(*) FROM duplicate_cases WHERE project_id=$1 AND decision='PENDING' AND submission_revision=$2`, p.ID, p.SubmissionRevision).Scan(&pending); err != nil {
				return err
			}
			if pending > 0 {
				code = "DUPLICATE_DECISION_REQUIRED"
			}
		}
		p.Actions[action] = hint(code)
	}
	return nil
}
