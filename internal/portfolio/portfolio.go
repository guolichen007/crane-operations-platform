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
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

var (
	ErrInvalidTransition = errors.New("invalid intake transition")
	ErrVersionConflict   = errors.New("intake version conflict")
	ErrNotReadyToApprove = errors.New("intake is not ready to approve")
	ErrAlreadyApproved   = errors.New("intake already has a formal project")
)

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

// CreateIntake inserts a REGISTERED intake. The tenant is derived from the
// portfolio scope, never from the request.
func CreateIntake(ctx context.Context, db *sql.DB, in contract.ProjectIntakeInput, registeredBy string) (contract.ProjectIntake, error) {
	var tenantID string
	if err := db.QueryRowContext(ctx, `SELECT tenant_id FROM portfolio_scopes WHERE id=$1`, in.PortfolioScopeId).Scan(&tenantID); err != nil {
		return contract.ProjectIntake{}, fmt.Errorf("portfolio scope: %w", err)
	}
	id := platform.ID()
	out := contract.ProjectIntake{
		Id:                   id,
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
	_, err := db.ExecContext(ctx, `INSERT INTO project_intakes(id,portfolio_scope_id,tenant_id,intake_code,project_name,customer_name,project_type,priority,risk_level,scope_summary,site_reference,registered_at,registered_by,requested_start,requested_handover,planned_start,planned_end,status,project_manager_user_id,technical_lead_user_id,site_lead_user_id,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
		out.Id, out.PortfolioScopeId, out.TenantId, out.IntakeCode, out.ProjectName, out.CustomerName, out.ProjectType, out.Priority, out.RiskLevel, out.ScopeSummary, out.SiteReference, out.RegisteredAt, out.RegisteredBy, out.RequestedStart, out.RequestedHandover, out.PlannedStart, out.PlannedEnd, out.Status, out.ProjectManagerUserId, out.TechnicalLeadUserId, out.SiteLeadUserId, out.Version)
	if err != nil {
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
