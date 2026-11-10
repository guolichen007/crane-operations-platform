// Package portfolio owns Portfolio-scoped project intake and the intake ->
// formal-project transition. A PortfolioGrant is narrow: it never grants
// project asset/equipment/telemetry access, and approving an intake never
// implicitly creates a ProjectGrant.
package portfolio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

var (
	ErrInvalidTransition = errors.New("invalid intake transition")
	ErrVersionConflict   = errors.New("intake version conflict")
	ErrNotReadyToApprove = errors.New("intake is not ready to approve")
	ErrAlreadyApproved   = errors.New("intake already has a formal project")
	// ErrInvalidIntake: the input violates the ProjectIntakeInput contract (HTTP 422).
	ErrInvalidIntake = errors.New("invalid intake input")
	// ErrDuplicateIntakeCode: the intake code already exists in this portfolio scope (HTTP 409).
	ErrDuplicateIntakeCode = errors.New("intake code already exists in this portfolio")
)

// ValidateIntakeInput checks the generated ProjectIntakeInput contract (lengths, enums,
// identifier pattern, RFC 3339 times) plus the business rules the schema cannot express:
// code, name and type must not be blank, and the date pairs must be ordered (the same
// rule as the table CHECKs). It never substitutes defaults.
func ValidateIntakeInput(in contract.ProjectIntakeInput) error {
	if err := contract.ValidateDocument("ProjectIntakeInput", in); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidIntake, err)
	}
	for field, value := range map[string]string{"intakeCode": in.IntakeCode, "projectName": in.ProjectName, "projectType": in.ProjectType} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalidIntake, field)
		}
	}
	if in.RequestedStart != nil && in.RequestedHandover != nil && !in.RequestedHandover.After(*in.RequestedStart) {
		return fmt.Errorf("%w: requestedHandover must be after requestedStart", ErrInvalidIntake)
	}
	if in.PlannedStart != nil && in.PlannedEnd != nil && !in.PlannedEnd.After(*in.PlannedStart) {
		return fmt.Errorf("%w: plannedEnd must be after plannedStart", ErrInvalidIntake)
	}
	return nil
}

// allowedTransitions is the frozen intake state machine. A bare PATCH status is
// forbidden; every change goes through TransitionIntake.
var allowedTransitions = map[string][]string{
	"REGISTERED": {"REVIEW", "REJECTED", "CANCELLED"},
	"REVIEW":     {"PLANNING", "REJECTED", "CANCELLED"},
	"PLANNING":   {"APPROVED", "REJECTED", "CANCELLED"},
	"APPROVED":   {},
	"REJECTED":   {},
	"CANCELLED":  {},
}

func validTransition(from, to string) bool {
	for _, next := range allowedTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// CreateIntake inserts a REGISTERED intake and its `intake_created` audit event in ONE
// transaction (no row without its audit, no audit without its row). The tenant is derived
// from the portfolio scope, never from the request.
func CreateIntake(ctx context.Context, db *sql.DB, in contract.ProjectIntakeInput, registeredBy, requestID string) (contract.ProjectIntake, error) {
	if err := ValidateIntakeInput(in); err != nil {
		return contract.ProjectIntake{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return contract.ProjectIntake{}, err
	}
	defer tx.Rollback()
	var tenantID string
	if err := tx.QueryRowContext(ctx, `SELECT tenant_id FROM portfolio_scopes WHERE id=$1`, in.PortfolioScopeId).Scan(&tenantID); err != nil {
		return contract.ProjectIntake{}, fmt.Errorf("portfolio scope: %w", err)
	}
	out := contract.ProjectIntake{
		Id:                   platform.ID(),
		PortfolioScopeId:     in.PortfolioScopeId,
		TenantId:             tenantID,
		IntakeCode:           in.IntakeCode,
		ProjectName:          in.ProjectName,
		CustomerName:         in.CustomerName,
		ProjectType:          in.ProjectType,
		Priority:             in.Priority,
		RiskLevel:            in.RiskLevel,
		ScopeSummary:         in.ScopeSummary,
		SiteReference:        in.SiteReference,
		RegisteredAt:         time.Now().UTC(),
		RegisteredBy:         registeredBy,
		RequestedStart:       in.RequestedStart,
		RequestedHandover:    in.RequestedHandover,
		PlannedStart:         in.PlannedStart,
		PlannedEnd:           in.PlannedEnd,
		Status:               "REGISTERED",
		ProjectManagerUserId: in.ProjectManagerUserId,
		TechnicalLeadUserId:  in.TechnicalLeadUserId,
		SiteLeadUserId:       in.SiteLeadUserId,
		Version:              1,
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO project_intakes(id,portfolio_scope_id,tenant_id,intake_code,project_name,customer_name,project_type,priority,risk_level,scope_summary,site_reference,registered_at,registered_by,requested_start,requested_handover,planned_start,planned_end,status,project_manager_user_id,technical_lead_user_id,site_lead_user_id,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
		out.Id, out.PortfolioScopeId, out.TenantId, out.IntakeCode, out.ProjectName, out.CustomerName, out.ProjectType, out.Priority, out.RiskLevel, out.ScopeSummary, out.SiteReference, out.RegisteredAt, out.RegisteredBy, out.RequestedStart, out.RequestedHandover, out.PlannedStart, out.PlannedEnd, out.Status, out.ProjectManagerUserId, out.TechnicalLeadUserId, out.SiteLeadUserId, out.Version)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "project_intakes_portfolio_scope_id_intake_code_key" {
			return contract.ProjectIntake{}, ErrDuplicateIntakeCode
		}
		return contract.ProjectIntake{}, err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: registeredBy, Action: "intake_created", Result: "success:" + out.Id + ":" + out.PortfolioScopeId, RequestID: requestID}); err != nil {
		return contract.ProjectIntake{}, err
	}
	if err = tx.Commit(); err != nil {
		return contract.ProjectIntake{}, err
	}
	return out, nil
}

// TransitionIntake applies an explicit state transition with actor/reason/
// request_id. It uses optimistic concurrency (version) and writes a transition
// record. A bare PATCH status is never accepted.
func TransitionIntake(ctx context.Context, db *sql.DB, intakeID, toStatus, actor, reason, requestID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT status,version FROM project_intakes WHERE id=$1 FOR UPDATE`, intakeID).Scan(&from, &version); err != nil {
		return err
	}
	if !validTransition(from, toStatus) {
		return ErrInvalidTransition
	}
	result, err := tx.ExecContext(ctx, `UPDATE project_intakes SET status=$1,version=version+1,updated_at=now() WHERE id=$2 AND version=$3`, toStatus, intakeID, version)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_intake_transitions(intake_id,from_status,to_status,actor,occurred_at,reason,request_id) VALUES($1,$2,$3,$4,now(),$5,$6)`, intakeID, from, toStatus, actor, reason, requestID); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Action: "intake_transition", Result: from + ":" + toStatus, RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// ApproveIntake transitions PLANNING -> APPROVED and creates the formal project
// in one transaction. It does not create a ProjectGrant: explicit project
// authorization remains a separate step.
func ApproveIntake(ctx context.Context, db *sql.DB, intakeID, actor, requestID string) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var status string
	var version int64
	var tenantID, name, portfolioScopeID string
	var formalID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status,version,tenant_id,project_name,portfolio_scope_id,formal_project_id FROM project_intakes WHERE id=$1 FOR UPDATE`, intakeID).Scan(&status, &version, &tenantID, &name, &portfolioScopeID, &formalID); err != nil {
		return "", err
	}
	if formalID.Valid {
		return formalID.String, ErrAlreadyApproved
	}
	if status != "PLANNING" {
		return "", ErrNotReadyToApprove
	}
	projectID := platform.ID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO projects(id,tenant_id,name,portfolio_scope_id,project_phase) VALUES($1,$2,$3,$4,'REGISTERED')`, projectID, tenantID, name, portfolioScopeID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_intakes SET status='APPROVED',formal_project_id=$1,version=version+1,updated_at=now() WHERE id=$2 AND version=$3`, projectID, intakeID, version); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_intake_transitions(intake_id,from_status,to_status,actor,occurred_at,reason,request_id) VALUES($1,'PLANNING','APPROVED',$2,now(),'intake approved',$3)`, intakeID, actor, requestID); err != nil {
		return "", err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "intake_approved", Result: "success", RequestID: requestID}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return projectID, nil
}

// ErrProjectNotManageable: the formal project does not exist, is not an approved intake's
// project, or belongs to another portfolio scope than the caller manages (HTTP 404).
var ErrProjectNotManageable = errors.New("formal project not found in a manageable portfolio scope")

const formalProjectSelect = `SELECT p.id,p.name,p.portfolio_scope_id,pi.id,pi.intake_code,p.project_phase,
 (SELECT count(*) FROM project_memberships m WHERE m.project_id=p.id),
 p.lifecycle IS NOT NULL, p.admin_code
FROM projects p JOIN project_intakes pi ON pi.formal_project_id=p.id AND pi.status='APPROVED'
 AND pi.portfolio_scope_id=p.portfolio_scope_id AND pi.tenant_id=p.tenant_id`

func scanFormal(scan func(...any) error) (contract.PortfolioProject, error) {
	var v contract.PortfolioProject
	var adminCode sql.NullInt64
	if err := scan(&v.Id, &v.Name, &v.PortfolioScopeId, &v.IntakeId, &v.IntakeCode, &v.ProjectPhase, &v.MemberCount, &v.InventoryRegistered, &adminCode); err != nil {
		return v, err
	}
	if adminCode.Valid {
		c := int(adminCode.Int64)
		v.AdminCode = &c
	}
	v.GrantStatus = "NO_MEMBERS"
	if v.MemberCount > 0 {
		v.GrantStatus = "MEMBERS_CONFIGURED"
	}
	return v, nil
}

// FormalProjects lists formal projects created by approved intakes in the given portfolio
// scopes only (the caller passes the scopes it is a member of). It never returns legacy or
// Inventory-only projects and never crosses scopes.
func FormalProjects(ctx context.Context, db *sql.DB, scopes []string) ([]contract.PortfolioProject, error) {
	out := []contract.PortfolioProject{}
	if len(scopes) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, formalProjectSelect+` WHERE p.portfolio_scope_id = ANY($1) ORDER BY pi.updated_at DESC, p.id`, scopes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanFormal(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// FormalProject loads one formal project and its owning scope; sql.ErrNoRows when the id is
// not an approved intake's project.
func FormalProject(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (contract.PortfolioProject, error) {
	return scanFormal(q.QueryRowContext(ctx, formalProjectSelect+` WHERE p.id=$1`, id).Scan)
}

// LockManageableProject is the in-transaction precondition for a grant change: it locks the
// formal project row and re-checks, inside the same transaction, that the project is an
// approved intake's project of `scope` and that `actor` still holds `cap` there. A membership
// revoked between the HTTP check and the write therefore cannot slip through.
func LockManageableProject(ctx context.Context, tx *sql.Tx, project, scope, actor string, allowed func(role string) bool) error {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT p.portfolio_scope_id FROM projects p JOIN project_intakes pi ON pi.formal_project_id=p.id AND pi.status='APPROVED'
 AND pi.portfolio_scope_id=p.portfolio_scope_id AND pi.tenant_id=p.tenant_id WHERE p.id=$1 FOR UPDATE OF p`, project).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != scope) {
		return ErrProjectNotManageable
	}
	if err != nil {
		return err
	}
	var role string
	err = tx.QueryRowContext(ctx, `SELECT m.role FROM portfolio_memberships m JOIN users u ON u.id=m.user_id
 WHERE m.user_id=$1 AND m.portfolio_scope_id=$2 AND u.enabled FOR SHARE OF m`, actor, scope).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !allowed(role)) {
		return ErrProjectNotManageable
	}
	return err
}
