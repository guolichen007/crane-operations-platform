package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/portfolio"
	"github.com/guolichen007/crane-operations-platform/internal/scheduling"
)

// authorizePortfolio checks one explicit portfolio capability. The scope is
// resolved from the child object, never trusted from the request body.
func (h *apiHandler) authorizePortfolio(w http.ResponseWriter, r *http.Request, scope string, cap identity.Capability) bool {
	if h.web == nil {
		return true
	}
	if err := identity.AuthorizePortfolioCapability(r.Context(), h.db, currentPrincipal(r).user, scope, cap); err != nil {
		h.deny(w, r, 403, "", "", "portfolio_scope")
		return false
	}
	return true
}

func portfolioScopeOfIntake(r *http.Request, db *sql.DB, intakeID string) (string, error) {
	var scope string
	err := db.QueryRowContext(r.Context(), `SELECT portfolio_scope_id FROM project_intakes WHERE id=$1`, intakeID).Scan(&scope)
	return scope, err
}

func portfolioScopeOfResource(r *http.Request, db *sql.DB, resourceID string) (string, error) {
	var scope string
	err := db.QueryRowContext(r.Context(), `SELECT portfolio_scope_id FROM resources WHERE id=$1`, resourceID).Scan(&scope)
	return scope, err
}

func portfolioScopeOfScheduleItem(r *http.Request, db *sql.DB, itemID string) (string, error) {
	var scope sql.NullString
	err := db.QueryRowContext(r.Context(), `SELECT portfolio_scope_id FROM schedule_items WHERE id=$1`, itemID).Scan(&scope)
	if err != nil {
		return "", err
	}
	if !scope.Valid {
		return "", errors.New("schedule item has no portfolio scope")
	}
	return scope.String, nil
}

// portfolioScopes returns the set of portfolio scopes the caller has a
// membership in; nil means "no filtering" (development mode).
func (h *apiHandler) portfolioScopes(r *http.Request) (map[string]bool, error) {
	if h.web == nil {
		return nil, nil
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT portfolio_scope_id FROM portfolio_memberships WHERE user_id=$1`, currentPrincipal(r).user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		scopes[s] = true
	}
	return scopes, rows.Err()
}

func (h *apiHandler) registerPortfolio(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/portfolio/scopes", func(w http.ResponseWriter, r *http.Request) {
		rows, err := h.db.QueryContext(r.Context(), `SELECT id,tenant_id,code,name,status,version FROM portfolio_scopes ORDER BY code`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		values := []contract.PortfolioScope{}
		for rows.Next() {
			var v contract.PortfolioScope
			if err := rows.Scan(&v.Id, &v.TenantId, &v.Code, &v.Name, &v.Status, &v.Version); err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/portfolio/intakes", func(w http.ResponseWriter, r *http.Request) {
		var in contract.ProjectIntakeInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if !h.authorizePortfolio(w, r, in.PortfolioScopeId, identity.CapProjectIntakeWrite) {
			return
		}
		out, err := portfolio.CreateIntake(r.Context(), h.db, in, currentPrincipal(r).user)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 201, out)
	})

	api.HandleFunc("GET /api/v1/portfolio/intakes", func(w http.ResponseWriter, r *http.Request) {
		scopes, err := h.portfolioScopes(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		rows, err := h.db.QueryContext(r.Context(), `SELECT id,portfolio_scope_id,tenant_id,intake_code,project_name,customer_name,project_type,priority,risk_level,scope_summary,site_reference,registered_at,registered_by,requested_start,requested_handover,planned_start,planned_end,status,project_manager_user_id,technical_lead_user_id,site_lead_user_id,formal_project_id,version FROM project_intakes ORDER BY registered_at DESC`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		values := []contract.ProjectIntake{}
		for rows.Next() {
			var v contract.ProjectIntake
			if err := rows.Scan(&v.Id, &v.PortfolioScopeId, &v.TenantId, &v.IntakeCode, &v.ProjectName, &v.CustomerName, &v.ProjectType, &v.Priority, &v.RiskLevel, &v.ScopeSummary, &v.SiteReference, &v.RegisteredAt, &v.RegisteredBy, &v.RequestedStart, &v.RequestedHandover, &v.PlannedStart, &v.PlannedEnd, &v.Status, &v.ProjectManagerUserId, &v.TechnicalLeadUserId, &v.SiteLeadUserId, &v.FormalProjectId, &v.Version); err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			if scopes != nil && !scopes[v.PortfolioScopeId] {
				continue
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/portfolio/intakes/{id}/transitions", func(w http.ResponseWriter, r *http.Request) {
		intakeID := r.PathValue("id")
		scope, err := portfolioScopeOfIntake(r, h.db, intakeID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolio(w, r, scope, identity.CapProjectIntakeWrite) {
			return
		}
		var in struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if err := portfolio.TransitionIntake(r.Context(), h.db, intakeID, in.Status, currentPrincipal(r).user, in.Reason, ""); err != nil {
			if errors.Is(err, portfolio.ErrInvalidTransition) {
				respondAPI(w, 409, map[string]string{"error": "invalid transition"})
				return
			}
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, map[string]string{"status": "transitioned"})
	})

	api.HandleFunc("POST /api/v1/portfolio/intakes/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		intakeID := r.PathValue("id")
		scope, err := portfolioScopeOfIntake(r, h.db, intakeID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolio(w, r, scope, identity.CapProjectApprove) {
			return
		}
		projectID, err := portfolio.ApproveIntake(r.Context(), h.db, intakeID, currentPrincipal(r).user, "")
		if err != nil {
			if errors.Is(err, portfolio.ErrNotReadyToApprove) || errors.Is(err, portfolio.ErrAlreadyApproved) {
				respondAPI(w, 409, map[string]string{"error": err.Error()})
				return
			}
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, map[string]string{"projectId": projectID})
	})

	api.HandleFunc("POST /api/v1/portfolio/resources", func(w http.ResponseWriter, r *http.Request) {
		var in contract.Resource
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if !h.authorizePortfolio(w, r, in.PortfolioScopeId, identity.CapResourceCalendarWrite) {
			return
		}
		out, err := scheduling.CreateResource(r.Context(), h.db, in)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 201, out)
	})

	api.HandleFunc("GET /api/v1/portfolio/resources", func(w http.ResponseWriter, r *http.Request) {
		rows, err := h.db.QueryContext(r.Context(), `SELECT id,portfolio_scope_id,resource_type,reference_id,name,capacity,exclusive,override_allowed,home_timezone,status,version FROM resources ORDER BY name`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		values := []contract.Resource{}
		for rows.Next() {
			var v contract.Resource
			if err := rows.Scan(&v.Id, &v.PortfolioScopeId, &v.ResourceType, &v.ReferenceId, &v.Name, &v.Capacity, &v.Exclusive, &v.OverrideAllowed, &v.HomeTimezone, &v.Status, &v.Version); err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("GET /api/v1/portfolio/resources/{id}/calendar", func(w http.ResponseWriter, r *http.Request) {
		resourceID := r.PathValue("id")
		scope, err := portfolioScopeOfResource(r, h.db, resourceID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolio(w, r, scope, identity.CapResourceCalendarRead) {
			return
		}
		var calID, tz string
		if err := h.db.QueryRowContext(r.Context(), `SELECT id,timezone FROM resource_calendars WHERE resource_id=$1`, resourceID).Scan(&calID, &tz); err != nil {
			respondAPI(w, 200, map[string]any{"timezone": "UTC", "rules": []any{}, "exceptions": []any{}})
			return
		}
		respondAPI(w, 200, map[string]string{"id": calID, "timezone": tz})
	})

	api.HandleFunc("POST /api/v1/portfolio/schedule/items", func(w http.ResponseWriter, r *http.Request) {
		var in contract.ScheduleItem
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if in.PortfolioScopeId != nil && !h.authorizePortfolio(w, r, *in.PortfolioScopeId, identity.CapProjectScheduleWrite) {
			return
		}
		out, err := scheduling.CreateScheduleItem(r.Context(), h.db, in)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 201, out)
	})

	api.HandleFunc("GET /api/v1/portfolio/schedule/items", func(w http.ResponseWriter, r *http.Request) {
		rows, err := h.db.QueryContext(r.Context(), `SELECT id,project_id,portfolio_scope_id,source_type,source_id,name,start_at,end_at,timezone,status,priority,exclusive_location_id,version FROM schedule_items ORDER BY start_at`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		values := []contract.ScheduleItem{}
		for rows.Next() {
			var v contract.ScheduleItem
			if err := rows.Scan(&v.Id, &v.ProjectId, &v.PortfolioScopeId, &v.SourceType, &v.SourceId, &v.Name, &v.StartAt, &v.EndAt, &v.Timezone, &v.Status, &v.Priority, &v.ExclusiveLocationId, &v.Version); err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/portfolio/schedule/items/{id}/reservations", func(w http.ResponseWriter, r *http.Request) {
		itemID := r.PathValue("id")
		scope, err := portfolioScopeOfScheduleItem(r, h.db, itemID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolio(w, r, scope, identity.CapProjectScheduleWrite) {
			return
		}
		var in contract.ResourceReservation
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		in.ScheduleItemId = itemID
		out, err := scheduling.CreateReservation(r.Context(), h.db, in)
		if err != nil {
			if errors.Is(err, scheduling.ErrCrossPortfolio) {
				respondAPI(w, 409, map[string]string{"error": "cross portfolio reservation"})
				return
			}
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 201, out)
	})

	api.HandleFunc("POST /api/v1/portfolio/schedule/approve", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ItemIds []string `json:"itemIds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.ItemIds) == 0 {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		for _, id := range in.ItemIds {
			scope, err := portfolioScopeOfScheduleItem(r, h.db, id)
			if err != nil {
				respondAPI(w, 404, map[string]string{"error": "not found"})
				return
			}
			if !h.authorizePortfolio(w, r, scope, identity.CapProjectScheduleWrite) {
				return
			}
		}
		conflicts, err := scheduling.ApproveSchedule(r.Context(), h.db, in.ItemIds, currentPrincipal(r).user, "")
		if err != nil {
			if errors.Is(err, scheduling.ErrHardConflict) {
				respondAPI(w, 409, conflicts)
				return
			}
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, map[string]string{"status": "approved"})
	})

	api.HandleFunc("GET /api/v1/portfolio/conflicts", func(w http.ResponseWriter, r *http.Request) {
		rows, err := h.db.QueryContext(r.Context(), `SELECT id,conflict_type,severity,left_item_id,right_item_id,resource_id,status FROM schedule_conflicts WHERE status='OPEN' ORDER BY detected_at DESC`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		values := []contract.ScheduleConflict{}
		for rows.Next() {
			var v contract.ScheduleConflict
			if err := rows.Scan(&v.Id, &v.ConflictType, &v.Severity, &v.LeftItemId, &v.RightItemId, &v.ResourceId, &v.Status); err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/portfolio/conflicts/{id}/override", func(w http.ResponseWriter, r *http.Request) {
		conflictID := r.PathValue("id")
		var in struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		var resourceID sql.NullString
		if err := h.db.QueryRowContext(r.Context(), `SELECT resource_id FROM schedule_conflicts WHERE id=$1`, conflictID).Scan(&resourceID); err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if resourceID.Valid {
			scope, err := portfolioScopeOfResource(r, h.db, resourceID.String)
			if err != nil {
				respondAPI(w, 404, map[string]string{"error": "not found"})
				return
			}
			if !h.authorizePortfolio(w, r, scope, identity.CapScheduleOverride) {
				return
			}
		}
		id, err := strconv.ParseInt(conflictID, 10, 64)
		if err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid conflict id"})
			return
		}
		if err := scheduling.OverrideConflict(r.Context(), h.db, id, in.Reason, currentPrincipal(r).user, ""); err != nil {
			if errors.Is(err, scheduling.ErrHardOverride) {
				respondAPI(w, 409, map[string]string{"error": "hard conflict cannot be overridden"})
				return
			}
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, map[string]string{"status": "overridden"})
	})

	api.HandleFunc("POST /api/v1/portfolio/baselines", func(w http.ResponseWriter, r *http.Request) {
		var in contract.ScheduleBaseline
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if !h.authorizePortfolio(w, r, in.PortfolioScopeId, identity.CapProjectScheduleWrite) {
			return
		}
		if _, err := scheduling.CreateBaseline(r.Context(), h.db, in, []byte("{}")); err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 201, map[string]string{"status": "created"})
	})
}
