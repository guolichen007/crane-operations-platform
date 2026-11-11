package app

// Q2: formal projects (created by approving an intake) and their initial ProjectGrants.
//
// Authorization model (docs/04-身份与权限/Phase1-A1.1-R2-PortfolioProject双域授权与Capability模型.md):
//   - list/detail: portfolio membership with portfolio_read in the project's OWN scope;
//   - members (read) and grant changes: project_grant_manage (portfolio_manager only) in the
//     project's own scope. platform_admin is NOT a bypass: a ProjectGrant never confers
//     portfolio rights, and the capability is not part of any project role;
//   - the project's scope is always loaded from the database (child id → project → scope);
//     an unknown id, a non-formal project or a project of another scope is 404 (no leak);
//   - the change itself is identity.ChangeGrantGuarded: same mutation + audit as the host
//     tool, plus an in-transaction re-check of scope/capability and an expected-role guard.

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/portfolio"
)

func (h *apiHandler) registerPortfolioProjects(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/portfolio/projects", func(w http.ResponseWriter, r *http.Request) {
		scopes, err := h.portfolioScopeRoles(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		ids := []string{}
		for scope, role := range scopes {
			if identity.HasPortfolioCapability(role, identity.CapPortfolioRead) {
				ids = append(ids, scope)
			}
		}
		values, err := portfolio.FormalProjects(r.Context(), h.db, ids)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		for i := range values {
			values[i].CanManageGrants = identity.HasPortfolioCapability(scopes[values[i].PortfolioScopeId], identity.CapProjectGrantManage)
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("GET /api/v1/portfolio/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := h.formalProjectFor(w, r, identity.CapPortfolioRead)
		if ok {
			respondAPI(w, 200, v)
		}
	})

	api.HandleFunc("GET /api/v1/portfolio/projects/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		v, ok := h.formalProjectFor(w, r, identity.CapProjectGrantManage)
		if !ok {
			return
		}
		members, err := identity.ProjectMembers(r.Context(), h.db, v.Id)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, toContractMembers(members))
	})

	// One endpoint for grant / role change / revoke: {userId, role|null, expectedRole|null}.
	// role=null revokes. expectedRole is the role the caller last saw (null = no membership).
	api.HandleFunc("POST /api/v1/portfolio/projects/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		v, ok := h.formalProjectFor(w, r, identity.CapProjectGrantManage)
		if !ok {
			return
		}
		var in contract.ProjectGrantChange
		if err := decodeStrict(r, &in); err != nil {
			if strings.HasPrefix(err.Error(), "json: unknown field") {
				respondAPI(w, 422, map[string]string{"error": "invalid grant change: unknown field"})
				return
			}
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if err := contract.ValidateDocument("ProjectGrantChange", in); err != nil {
			respondAPI(w, 422, map[string]string{"error": "invalid grant change: " + err.Error()})
			return
		}
		p := currentPrincipal(r)
		if in.UserId == p.user {
			// A manager cannot grant themselves project rights: a second manager must do it.
			respondAPI(w, 422, map[string]string{"error": "invalid grant change: cannot change your own project membership"})
			return
		}
		change := identity.GrantChange{User: in.UserId, Project: v.Id, Revoke: in.Role == nil}
		if in.Role != nil {
			change.Role = *in.Role
		}
		if in.ExpectedRole != nil {
			change.ExpectedRole = *in.ExpectedRole
		}
		if h.web != nil {
			change.Issuer = h.web.Issuer()
		}
		event := p.meta.Event(p.user, v.Id, "", "", "")
		event.RequestID = requestIDOr(r, event.RequestID)
		err := identity.ChangeGrantGuarded(r.Context(), h.db, change, event, func(tx *sql.Tx) error {
			return portfolio.LockManageableProject(r.Context(), tx, v.Id, v.PortfolioScopeId, p.user, func(role string) bool {
				return identity.HasPortfolioCapability(role, identity.CapProjectGrantManage)
			})
		})
		switch {
		case err == nil:
		case errors.Is(err, identity.ErrGrantTargetInvalid):
			respondAPI(w, 422, map[string]string{"error": err.Error()})
			return
		case errors.Is(err, identity.ErrGrantConflict), errors.Is(err, identity.ErrGrantNoop):
			respondAPI(w, 409, map[string]string{"error": err.Error()})
			return
		case errors.Is(err, portfolio.ErrProjectNotManageable):
			h.deny(w, r, 404, v.Id, "", "portfolio_grant_scope")
			return
		default:
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		// The change is COMMITTED. A failure to re-read the member list must never turn that into
		// an error response (the client would retry an already-applied change): answer 200
		// APPLIED with membersRefreshed=false and let the client reload the list separately.
		result := contract.ProjectGrantResult{Result: "APPLIED", Members: []contract.ProjectMember{}}
		if members, err := h.membersAfterChange(r, v.Id); err == nil {
			result.Members, result.MembersRefreshed = toContractMembers(members), true
		}
		respondAPI(w, 200, result)
	})
}

// membersAfterChange re-reads the member list after a committed change. Tests can inject a
// failure through apiHandler.membersReadHook to prove a committed change is still reported.
func (h *apiHandler) membersAfterChange(r *http.Request, project string) ([]identity.ProjectMember, error) {
	if h.membersReadHook != nil {
		if err := h.membersReadHook(); err != nil {
			return nil, err
		}
	}
	return identity.ProjectMembers(r.Context(), h.db, project)
}

// formalProjectFor resolves {id} → formal project → owning scope from the DB and checks one
// portfolio capability there. Non-formal / unknown / foreign-scope ids are 404; membership
// without the capability is 403; any failure to decide is 503.
func (h *apiHandler) formalProjectFor(w http.ResponseWriter, r *http.Request, cap identity.Capability) (contract.PortfolioProject, bool) {
	id := r.PathValue("id")
	if !contract.ValidID(id) {
		respondAPI(w, 404, map[string]string{"error": "not found"})
		return contract.PortfolioProject{}, false
	}
	v, err := portfolio.FormalProject(r.Context(), h.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		h.deny(w, r, 404, "", "", "portfolio_scope")
		return v, false
	}
	if err != nil {
		respondAPI(w, 503, map[string]string{"error": "database unavailable"})
		return v, false
	}
	if !h.authorizePortfolioObject(w, r, v.PortfolioScopeId, cap) {
		return v, false
	}
	if h.web == nil {
		v.CanManageGrants = true
		return v, true
	}
	role, err := identity.PortfolioRole(r.Context(), h.db, currentPrincipal(r).user, v.PortfolioScopeId)
	if err != nil {
		respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
		return v, false
	}
	v.CanManageGrants = identity.HasPortfolioCapability(role, identity.CapProjectGrantManage)
	return v, true
}

// portfolioScopeRoles maps the caller's portfolio scopes to their role (enabled users only).
func (h *apiHandler) portfolioScopeRoles(r *http.Request) (map[string]string, error) {
	out := map[string]string{}
	if h.web == nil {
		rows, err := h.db.QueryContext(r.Context(), `SELECT id FROM portfolio_scopes`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return nil, err
			}
			out[s] = "portfolio_manager"
		}
		return out, rows.Err()
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT m.portfolio_scope_id,m.role FROM portfolio_memberships m JOIN users u ON u.id=m.user_id WHERE m.user_id=$1 AND u.enabled`, currentPrincipal(r).user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s, role string
		if err := rows.Scan(&s, &role); err != nil {
			return nil, err
		}
		out[s] = role
	}
	return out, rows.Err()
}

func toContractMembers(in []identity.ProjectMember) []contract.ProjectMember {
	out := make([]contract.ProjectMember, 0, len(in))
	for _, m := range in {
		out = append(out, contract.ProjectMember{UserId: m.UserID, DisplayName: m.DisplayName, Role: m.Role, Enabled: m.Enabled})
	}
	return out
}

func requestIDOr(r *http.Request, fallback string) string {
	if id := requestID(r); id != "" {
		return id
	}
	return fallback
}
