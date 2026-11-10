package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// authorizePortfolioObject resolves a direct child object's portfolio and checks
// one capability. A caller with no membership in the owning scope gets 404 (the
// object's existence is not leaked); a caller with membership but insufficient
// capability gets 403.
func (h *apiHandler) authorizePortfolioObject(w http.ResponseWriter, r *http.Request, scope string, cap identity.Capability) bool {
	if h.web == nil {
		return true
	}
	role, err := identity.PortfolioRole(r.Context(), h.db, currentPrincipal(r).user, scope)
	if errors.Is(err, identity.ErrDenied) {
		h.deny(w, r, 404, "", "", "portfolio_scope")
		return false
	}
	if err != nil {
		respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
		return false
	}
	if !identity.HasPortfolioCapability(role, cap) {
		h.deny(w, r, 403, "", "", "portfolio_role")
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

// portfolioScopeOfConflict resolves a conflict's owning portfolio via its left
// schedule item (left_item_id is NOT NULL), falling back to the resource scope.
func portfolioScopeOfConflict(r *http.Request, db *sql.DB, conflictID string) (string, error) {
	var scope sql.NullString
	err := db.QueryRowContext(r.Context(), `SELECT COALESCE(si.portfolio_scope_id, r.portfolio_scope_id) FROM schedule_conflicts sc LEFT JOIN schedule_items si ON si.id=sc.left_item_id LEFT JOIN resources r ON r.id=sc.resource_id WHERE sc.id=$1`, conflictID).Scan(&scope)
	if err != nil {
		return "", err
	}
	if !scope.Valid {
		return "", errors.New("conflict has no portfolio scope")
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
	h.registerPortfolioProjects(api)
	api.HandleFunc("GET /api/v1/portfolio/scopes", func(w http.ResponseWriter, r *http.Request) {
		memberships, err := h.portfolioScopes(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		// The caller's own role per scope (UX only: e.g. hide the approve button). Every
		// mutation still re-checks the capability server-side.
		roles, err := h.portfolioScopeRoles(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
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
			if memberships != nil && !memberships[v.Id] {
				continue
			}
			if role, ok := roles[v.Id]; ok {
				v.MyRole = &role
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/portfolio/intakes", func(w http.ResponseWriter, r *http.Request) {
		var in contract.ProjectIntakeInput
		if err := decodeStrict(r, &in); err != nil {
			// Unknown fields are a contract violation (422); unreadable JSON stays 400.
			if strings.HasPrefix(err.Error(), "json: unknown field") {
				respondAPI(w, 422, map[string]string{"error": "invalid intake input: unknown field"})
				return
			}
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if !h.authorizePortfolio(w, r, in.PortfolioScopeId, identity.CapProjectIntakeWrite) {
			return
		}
		out, err := portfolio.CreateIntake(r.Context(), h.db, in, currentPrincipal(r).user, requestID(r))
		if err != nil {
			switch {
			case errors.Is(err, portfolio.ErrInvalidIntake):
				respondAPI(w, 422, map[string]string{"error": err.Error()})
			case errors.Is(err, portfolio.ErrDuplicateIntakeCode):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
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

	api.HandleFunc("GET /api/v1/portfolio/intakes/{id}", func(w http.ResponseWriter, r *http.Request) {
		intakeID := r.PathValue("id")
		scope, err := portfolioScopeOfIntake(r, h.db, intakeID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapProjectIntakeRead) {
			return
		}
		var v contract.ProjectIntake
		if err := h.db.QueryRowContext(r.Context(), `SELECT id,portfolio_scope_id,tenant_id,intake_code,project_name,customer_name,project_type,priority,risk_level,scope_summary,site_reference,registered_at,registered_by,requested_start,requested_handover,planned_start,planned_end,status,project_manager_user_id,technical_lead_user_id,site_lead_user_id,formal_project_id,version FROM project_intakes WHERE id=$1`, intakeID).Scan(&v.Id, &v.PortfolioScopeId, &v.TenantId, &v.IntakeCode, &v.ProjectName, &v.CustomerName, &v.ProjectType, &v.Priority, &v.RiskLevel, &v.ScopeSummary, &v.SiteReference, &v.RegisteredAt, &v.RegisteredBy, &v.RequestedStart, &v.RequestedHandover, &v.PlannedStart, &v.PlannedEnd, &v.Status, &v.ProjectManagerUserId, &v.TechnicalLeadUserId, &v.SiteLeadUserId, &v.FormalProjectId, &v.Version); err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		respondAPI(w, 200, v)
	})

	api.HandleFunc("POST /api/v1/portfolio/intakes/{id}/transitions", func(w http.ResponseWriter, r *http.Request) {
		intakeID := r.PathValue("id")
		scope, err := portfolioScopeOfIntake(r, h.db, intakeID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapProjectIntakeWrite) {
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
			if errors.Is(err, portfolio.ErrVersionConflict) {
				respondAPI(w, 409, map[string]string{"error": "intake changed concurrently; reload and retry"})
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
		if !h.authorizePortfolioObject(w, r, scope, identity.CapProjectApprove) {
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
		if !h.authorizePortfolio(w, r, in.PortfolioScopeId, identity.CapProjectScheduleWrite) {
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
		scopes, err := h.portfolioScopes(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
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
			if scopes != nil && !scopes[v.PortfolioScopeId] {
				continue
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("GET /api/v1/portfolio/resources/{id}", func(w http.ResponseWriter, r *http.Request) {
		resourceID := r.PathValue("id")
		scope, err := portfolioScopeOfResource(r, h.db, resourceID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapPortfolioRead) {
			return
		}
		var v contract.Resource
		if err := h.db.QueryRowContext(r.Context(), `SELECT id,portfolio_scope_id,resource_type,reference_id,name,capacity,exclusive,override_allowed,home_timezone,status,version FROM resources WHERE id=$1`, resourceID).Scan(&v.Id, &v.PortfolioScopeId, &v.ResourceType, &v.ReferenceId, &v.Name, &v.Capacity, &v.Exclusive, &v.OverrideAllowed, &v.HomeTimezone, &v.Status, &v.Version); err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		respondAPI(w, 200, v)
	})

	api.HandleFunc("GET /api/v1/portfolio/resources/{id}/calendar", func(w http.ResponseWriter, r *http.Request) {
		resourceID := r.PathValue("id")
		scope, err := portfolioScopeOfResource(r, h.db, resourceID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapResourceCalendarRead) {
			return
		}
		var calID, tz string
		if err := h.db.QueryRowContext(r.Context(), `SELECT id,timezone FROM resource_calendars WHERE resource_id=$1`, resourceID).Scan(&calID, &tz); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				respondAPI(w, 200, map[string]any{"timezone": "UTC", "rules": []any{}, "exceptions": []any{}})
				return
			}
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, map[string]string{"id": calID, "timezone": tz})
	})

	api.HandleFunc("PUT /api/v1/portfolio/resources/{id}/calendar", func(w http.ResponseWriter, r *http.Request) {
		resourceID := r.PathValue("id")
		scope, err := portfolioScopeOfResource(r, h.db, resourceID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapResourceCalendarWrite) {
			return
		}
		var in struct {
			Timezone string                    `json:"timezone"`
			Version  int64                     `json:"version"`
			Rules    []scheduling.CalendarRule `json:"rules"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid timezone"})
			return
		}
		if err := scheduling.ReplaceCalendar(r.Context(), h.db, resourceID, in.Timezone, in.Rules, in.Version); err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, map[string]string{"status": "updated"})
	})

	api.HandleFunc("POST /api/v1/portfolio/schedule/dependencies", func(w http.ResponseWriter, r *http.Request) {
		var in contract.ScheduleDependency
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if in.DependencyType != "FS" {
			respondAPI(w, 422, map[string]string{"error": "unsupported dependency type"})
			return
		}
		if err := scheduling.CreateDependency(r.Context(), h.db, in); err != nil {
			if errors.Is(err, scheduling.ErrDependencyCycle) {
				respondAPI(w, 409, map[string]string{"error": "dependency cycle"})
				return
			}
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 201, map[string]string{"status": "created"})
	})

	api.HandleFunc("GET /api/v1/portfolio/schedule/dependencies", func(w http.ResponseWriter, r *http.Request) {
		rows, err := h.db.QueryContext(r.Context(), `SELECT id,predecessor_item_id,successor_item_id,dependency_type,lag_minutes FROM schedule_dependencies ORDER BY id`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		values := []contract.ScheduleDependency{}
		for rows.Next() {
			var v contract.ScheduleDependency
			if err := rows.Scan(&v.Id, &v.PredecessorItemId, &v.SuccessorItemId, &v.DependencyType, &v.LagMinutes); err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/portfolio/schedule/items", func(w http.ResponseWriter, r *http.Request) {
		var in contract.ScheduleItem
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		// Authoritative scope resolution, never a nil-scope authorization bypass.
		scope, err := scheduling.ResolveScheduleScope(r.Context(), h.db, in.ProjectId, in.PortfolioScopeId)
		if err != nil {
			respondAPI(w, 409, map[string]string{"error": err.Error()})
			return
		}
		if !h.authorizePortfolio(w, r, scope, identity.CapProjectScheduleWrite) {
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
		scopes, err := h.portfolioScopes(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
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
			if scopes != nil && (v.PortfolioScopeId == nil || !scopes[*v.PortfolioScopeId]) {
				continue
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("GET /api/v1/portfolio/schedule/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		itemID := r.PathValue("id")
		scope, err := portfolioScopeOfScheduleItem(r, h.db, itemID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapProjectScheduleRead) {
			return
		}
		var v contract.ScheduleItem
		if err := h.db.QueryRowContext(r.Context(), `SELECT id,project_id,portfolio_scope_id,source_type,source_id,name,start_at,end_at,timezone,status,priority,exclusive_location_id,version FROM schedule_items WHERE id=$1`, itemID).Scan(&v.Id, &v.ProjectId, &v.PortfolioScopeId, &v.SourceType, &v.SourceId, &v.Name, &v.StartAt, &v.EndAt, &v.Timezone, &v.Status, &v.Priority, &v.ExclusiveLocationId, &v.Version); err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		respondAPI(w, 200, v)
	})

	api.HandleFunc("POST /api/v1/portfolio/schedule/items/{id}/reservations", func(w http.ResponseWriter, r *http.Request) {
		itemID := r.PathValue("id")
		scope, err := portfolioScopeOfScheduleItem(r, h.db, itemID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapProjectScheduleWrite) {
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
			if !h.authorizePortfolioObject(w, r, scope, identity.CapProjectScheduleWrite) {
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
		scopes, err := h.portfolioScopes(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		rows, err := h.db.QueryContext(r.Context(), `SELECT sc.id,sc.conflict_type,sc.severity,sc.left_item_id,sc.right_item_id,sc.resource_id,sc.status,COALESCE(si.portfolio_scope_id,r.portfolio_scope_id) FROM schedule_conflicts sc LEFT JOIN schedule_items si ON si.id=sc.left_item_id LEFT JOIN resources r ON r.id=sc.resource_id WHERE sc.status='OPEN' ORDER BY sc.detected_at DESC`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		values := []contract.ScheduleConflict{}
		for rows.Next() {
			var v contract.ScheduleConflict
			var owningScope sql.NullString
			if err := rows.Scan(&v.Id, &v.ConflictType, &v.Severity, &v.LeftItemId, &v.RightItemId, &v.ResourceId, &v.Status, &owningScope); err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			if scopes != nil && (!owningScope.Valid || !scopes[owningScope.String]) {
				continue
			}
			values = append(values, v)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("GET /api/v1/portfolio/conflicts/{id}", func(w http.ResponseWriter, r *http.Request) {
		conflictID := r.PathValue("id")
		scope, err := portfolioScopeOfConflict(r, h.db, conflictID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapProjectScheduleRead) {
			return
		}
		var v contract.ScheduleConflict
		if err := h.db.QueryRowContext(r.Context(), `SELECT id,conflict_type,severity,left_item_id,right_item_id,resource_id,status FROM schedule_conflicts WHERE id=$1`, conflictID).Scan(&v.Id, &v.ConflictType, &v.Severity, &v.LeftItemId, &v.RightItemId, &v.ResourceId, &v.Status); err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		respondAPI(w, 200, v)
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
		scope, err := portfolioScopeOfConflict(r, h.db, conflictID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizePortfolioObject(w, r, scope, identity.CapScheduleOverride) {
			return
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

	api.HandleFunc("GET /api/v1/portfolio/baselines", func(w http.ResponseWriter, r *http.Request) {
		scopes, err := h.portfolioScopes(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		rows, err := h.db.QueryContext(r.Context(), `SELECT id,portfolio_scope_id,project_id,name,baseline_version,approved_by,approved_at FROM schedule_baselines ORDER BY approved_at DESC`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		values := []contract.ScheduleBaseline{}
		for rows.Next() {
			var v contract.ScheduleBaseline
			if err := rows.Scan(&v.Id, &v.PortfolioScopeId, &v.ProjectId, &v.Name, &v.BaselineVersion, &v.ApprovedBy, &v.ApprovedAt); err != nil {
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
