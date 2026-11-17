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
		// A supervisor's own filings carry the supervisor's team (create and
		// submit require it; a team change requires a handoff first), so the
		// team scope already contains them.
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

// filerRole: roles that may file and hold their own projects. V1.1 opens own
// filings to supervisors; directors still cannot file for themselves.
func filerRole(role string) bool { return role == salesRole || role == managerRole }

func (s ProjectPolicy) Create() bool {
	return s.User.Role == adminRole || filerRole(s.User.Role) && contains(s.User.Permissions, "project.create")
}
func (s ProjectPolicy) Manage() bool { return manager(s.User) || s.User.Role == adminRole }

// Review routes (V1.1 R1). A sales member's filing is decided by a supervisor
// of the team (or a director); a supervisor's own filing only by a director.
// The effective route never drops below the route frozen with the submitted
// revision, and it is DIRECTOR whenever the current owner is not a sales member.
const (
	routeTeamManager = "TEAM_MANAGER"
	routeDirector    = "DIRECTOR"
)

func effectiveRoute(p Project) string {
	if p.OwnerSubject == "" {
		return ""
	}
	if p.routeSnapshot == routeDirector || p.OwnerRole != salesRole {
		return routeDirector
	}
	return routeTeamManager
}

// reviewRouteSQL is the SQL form of effectiveRoute for alias p.
func reviewRouteSQL(p string) string {
	return `(CASE WHEN ` + p + `.owner_subject IS NULL THEN '' WHEN EXISTS(SELECT 1 FROM project_review_routes rr WHERE rr.project_id=` + p + `.id
 AND rr.submission_revision=` + p + `.submission_revision AND rr.route='DIRECTOR')
 OR coalesce((SELECT ru.role FROM app_users ru WHERE ru.subject=` + p + `.owner_subject),'')<>'baobei_sales' THEN 'DIRECTOR' ELSE 'TEAM_MANAGER' END)`
}

func (s ProjectPolicy) Action(p Project, action string) string {
	u := s.User
	if !s.Read(p) {
		return "NOT_FOUND"
	}
	if p.OwnerSubject == "" && p.IntakeStatus != "DRAFT" && p.DiscardedAt == nil {
		return "INVALID_STATE"
	}
	// Only the owner writes their project; seeing a team project (supervisor)
	// gives review rights, never edit rights.
	own := filerRole(u.Role) && p.OwnerSubject == u.Subject
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
		// A supervisor hands off their own projects and those of the team's sales
		// members, never another supervisor's own projects.
		if u.Role == managerRole && p.OwnerSubject != u.Subject && p.OwnerRole != salesRole {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if !editable(p.IntakeStatus) && !contains([]string{"SUBMITTED", "UNDER_REVIEW", "APPROVED"}, p.IntakeStatus) {
			return "INVALID_STATE"
		}
	case "discard":
		// Only a draft that was never submitted: submitted, reviewed and evidence
		// records are never removed.
		if !writer {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if p.IntakeStatus != "DRAFT" || p.SubmissionRevision > 0 || p.DiscardedAt != nil {
			return "INVALID_STATE"
		}
	case "restore":
		if !writer {
			return "BUSINESS_PERMISSION_DENIED"
		}
		if p.IntakeStatus != "CANCELLED" || p.DiscardedAt == nil {
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
		if u.Role == managerRole && effectiveRoute(p) == routeDirector {
			return "REVIEW_ROUTE_DIRECTOR"
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

// activeOwner: a legal assignment target (administrator assignment, project
// transfer, staff handoff): an active sales member with a team.
func activeOwner(ctx context.Context, q querier, subject string) (string, error) {
	return ownerTeam(ctx, q, subject, []string{salesRole})
}

// selfOwner: a legal holder of their own filing (create, submit): an active
// sales member or supervisor with a team (V1.1). Archiving a team requires
// that no member is left in it, so an active member never sits in an archived team.
func selfOwner(ctx context.Context, q querier, subject string) (string, error) {
	return ownerTeam(ctx, q, subject, []string{salesRole, managerRole})
}

func ownerTeam(ctx context.Context, q querier, subject string, roles []string) (string, error) {
	if subject == "" {
		return "", problem(409, "OWNER_REQUIRED")
	}
	var team string
	err := q.QueryRow(ctx, `SELECT u.team_id::text FROM app_users u WHERE u.team_id IS NOT NULL AND u.subject=$1 AND u.active AND u.role=ANY($2)`, subject, roles).Scan(&team)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", problem(409, "OWNER_INACTIVE")
	}
	return team, err
}

// freezeRoute writes the immutable review route of the revision just frozen.
func freezeRoute(ctx context.Context, tx pgx.Tx, p Project, revision int) error {
	tag, err := tx.Exec(ctx, `INSERT INTO project_review_routes(project_id,submission_revision,owner_subject,owner_role,team_id,route,basis)
 SELECT $1,$2,u.subject,u.role,$3::uuid,CASE WHEN u.role='baobei_sales' THEN 'TEAM_MANAGER' ELSE 'DIRECTOR' END,'SUBMISSION'
 FROM app_users u WHERE u.subject=$4`, p.ID, revision, p.TeamID, p.OwnerSubject)
	if err == nil && tag.RowsAffected() != 1 {
		return problem(409, "OWNER_INACTIVE")
	}
	return err
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
