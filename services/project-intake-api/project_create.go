package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"regexp"
)

type createProjectInput struct {
	Input
	AssignmentMode string `json:"assignmentMode,omitempty"`
	TargetSubject  string `json:"targetSubject,omitempty"`
}

var createKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func (a *App) createProject(w http.ResponseWriter, r *http.Request, u User) error {
	ctx := r.Context()
	if !(ProjectPolicy{u}).Create() {
		return problem(403, "BUSINESS_PERMISSION_DENIED")
	}
	var in createProjectInput
	if !decode(w, r, &in) {
		return nil
	}
	if err := validateInput(&in.Input, false); err != nil {
		fail(w, 400, "INVALID_INPUT", err.Error())
		return nil
	}
	in.Version = 1
	if u.Role == salesRole && (in.AssignmentMode != "" || in.TargetSubject != "") {
		return problem(403, "BUSINESS_PERMISSION_DENIED")
	}
	if u.Role == adminRole {
		if in.AssignmentMode == "" {
			in.AssignmentMode = "UNASSIGNED_DRAFT"
		}
		if in.AssignmentMode != "UNASSIGNED_DRAFT" && in.AssignmentMode != "ASSIGN_ACTIVE_SALES" || in.AssignmentMode == "UNASSIGNED_DRAFT" && in.TargetSubject != "" {
			return problem(400, "INVALID_ASSIGNMENT")
		}
	}
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || !createKeyPattern.MatchString(key) {
		return problem(400, "IDEMPOTENCY_KEY_REQUIRED")
	}
	requestHash := hash(string(mustJSON(in)))
	tx, err := a.beginBusiness(ctx, u, 1)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, u.Subject+"|"+key); err != nil {
		return err
	}
	var priorHash, id string
	err = tx.QueryRow(ctx, `SELECT request_hash,project_id::text FROM project_create_requests WHERE actor_subject=$1 AND idempotency_key=$2`, u.Subject, key).Scan(&priorHash, &id)
	if err == nil {
		if priorHash != requestHash {
			return problem(409, "IDEMPOTENCY_CONFLICT")
		}
		p, e := get(ctx, tx, id, false)
		if e != nil {
			return e
		}
		if !canRead(u, p) {
			return problem(404, "NOT_FOUND")
		}
		p.Replayed = true
		if e = a.projectHints(ctx, tx, u, &p); e != nil {
			return e
		}
		respond(w, 200, p)
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	owner, team := "", ""
	if u.Role == salesRole {
		owner = u.Subject
	} else if in.AssignmentMode == "ASSIGN_ACTIVE_SALES" {
		owner = in.TargetSubject
		if owner == "" {
			return problem(400, "INVALID_TARGET")
		}
	}
	if owner != "" {
		team, err = activeOwner(ctx, tx, owner)
		if err != nil {
			return err
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,sales_stage,information_source,confidence_level,project_type,estimated_amount,expected_purchase_date,document,evidence_state,protection_basis,protection_status,next_action,next_followup_at)
 VALUES($1,$2,NULLIF($3,''),$4,$4,NULLIF($5,'')::uuid,$6,$7,$8,$9,$10,NULLIF($11,'')::date,$12,'COMPLETE','FIRST_SUBMISSION','NOT_STARTED',$13,$14) RETURNING id::text`, in.ProjectName, normalize(in.ProjectName), owner, u.Subject, team, in.SalesStage, in.InformationSource, in.ConfidenceLevel, in.ProjectType, in.EstimatedAmount, in.ExpectedPurchaseDate, mustJSON(in.Input), in.NextAction, in.NextFollowupAt).Scan(&id)
	if err != nil {
		return err
	}
	p, err := get(ctx, tx, id, false)
	if err != nil {
		return err
	}
	if err = relations(ctx, tx, p); err != nil {
		return err
	}
	if err = participation(ctx, tx, p, u, "CREATE"); err != nil {
		return err
	}
	event := "project.create_unassigned"
	if owner != "" {
		event = "project.create"
		if _, err = tx.Exec(ctx, `INSERT INTO ownership_history(project_id,to_subject,reason,actor_subject) VALUES($1,$2,'首次创建，由服务端确定',$3)`, id, owner, u.Subject); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, p, u, event); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_create_requests(actor_subject,idempotency_key,request_hash,project_id,created_version) VALUES($1,$2,$3,$4,1)`, u.Subject, key, requestHash, id); err != nil {
		return err
	}
	if err = a.projectHints(ctx, tx, u, &p); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	respond(w, 200, p)
	return nil
}

func relatedReviewProjects(ctx context.Context, tx pgx.Tx, id string) ([]Project, error) {
	// Submission and all duplicate decisions share 727102. Lock all affected projects in UUID order.
	// The lock statement is unchanged since G1; G3-2C only replaced the
	// per-project reads that followed it with one read of the locked set.
	rows, err := tx.Query(ctx, `SELECT id::text FROM intake_projects WHERE id=$1 OR id IN (SELECT candidate_id FROM duplicate_cases WHERE project_id=$1 AND (submission_revision=(SELECT submission_revision FROM intake_projects WHERE id=$1) OR decision<>'PENDING')) ORDER BY id FOR UPDATE`, id)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return []Project{}, err
	}
	rows, err = tx.Query(ctx, `SELECT `+columns+` FROM intake_projects p WHERE p.id=ANY($1::uuid[]) ORDER BY p.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// createReceipt lets the original actor recover a project created with a
// client intent key after the response was lost, without resending the body.
// It never reveals whether another account used the same key.
func (a *App) createReceipt(w http.ResponseWriter, r *http.Request, u User, key string) error {
	ctx := r.Context()
	if !createKeyPattern.MatchString(key) {
		return problem(400, "IDEMPOTENCY_KEY_REQUIRED")
	}
	var id string
	var created int
	err := a.db.QueryRow(ctx, `SELECT c.project_id::text,c.created_version FROM project_create_requests c JOIN app_sessions s ON s.subject=c.actor_subject
 WHERE c.actor_subject=$1 AND c.idempotency_key=$2 AND s.token_hash=$3 AND s.expires_at>now()`, u.Subject, key, u.sessionHash).Scan(&id, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return problem(404, "CREATE_RECEIPT_NOT_FOUND")
	}
	if err != nil {
		return err
	}
	p, err := get(ctx, a.db, id, false)
	if err != nil {
		return err
	}
	// A creator who has since lost read scope (e.g. the project was handed to
	// another sales owner) only learns that no receipt is available.
	if !canRead(u, p) {
		return problem(404, "CREATE_RECEIPT_NOT_FOUND")
	}
	if err = a.projectHints(ctx, a.db, u, &p); err != nil {
		return err
	}
	p.Replayed = true
	respond(w, 200, map[string]any{"project": p, "createdVersion": created})
	return nil
}
