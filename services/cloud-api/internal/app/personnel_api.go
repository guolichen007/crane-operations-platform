package app

// P1 personnel / account / permission management HTTP surface (/api/v1/admin/*).
//
// Every route requires a valid BFF session (+ CSRF for writes) through the common /api/ handler;
// the system capability is then re-checked by internal/personnel against the database (inside the
// mutation transaction for writes). A system role never grants project or portfolio business
// access, and project / portfolio roles never grant these routes. Responses carrying a one-time
// password are no-store and the password is never logged or persisted.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/personnel"
)

func (h *apiHandler) actor(r *http.Request) personnel.Actor {
	p := currentPrincipal(r)
	event := p.meta.Event(p.user, "", "", "", "")
	event.RequestID = requestIDOr(r, event.RequestID)
	return personnel.Actor{ID: p.user, Event: event}
}

// personnelError maps a personnel error to an HTTP answer (denials are audited).
func (h *apiHandler) personnelError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid personnel.InvalidError
	switch {
	case errors.As(err, &invalid):
		respondAPI(w, 422, map[string]string{"error": invalid.Message, "field": invalid.Field})
	case errors.Is(err, personnel.ErrForbidden):
		h.deny(w, r, 403, "", "", "system_role")
	case errors.Is(err, personnel.ErrProtected):
		h.deny(w, r, 403, "", "", "system_role_target")
	case errors.Is(err, personnel.ErrNotFound):
		respondAPI(w, 404, map[string]string{"error": "not found"})
	case errors.Is(err, personnel.ErrLastAdmin):
		respondAPI(w, 409, map[string]string{"error": "至少需要保留一名启用状态的系统管理员", "code": "LAST_SYSTEM_ADMIN"})
	case errors.Is(err, personnel.ErrConflict):
		respondAPI(w, 409, map[string]string{"error": strings.TrimPrefix(err.Error(), "conflict: ")})
	case errors.Is(err, personnel.ErrBusy):
		respondAPI(w, 409, map[string]string{"error": "该人员的另一项账号操作正在进行，请稍后刷新再试", "code": "PERSON_BUSY"})
	case errors.Is(err, personnel.ErrProvisionIncomplete):
		respondAPI(w, 409, map[string]string{"error": "该账号的开户尚未完成，请在「未完成的开户」中继续或放弃", "code": "PROVISION_INCOMPLETE"})
	case errors.Is(err, personnel.ErrSelf):
		respondAPI(w, 422, map[string]string{"error": "不能对自己的账号执行此操作，请由另一位系统管理员处理", "code": "SELF"})
	case errors.Is(err, personnel.ErrIdempotencyKey):
		respondAPI(w, 422, map[string]string{"error": "幂等键已用于另一请求，请刷新后重新提交", "code": "IDEMPOTENCY_KEY"})
	case errors.Is(err, personnel.ErrIdentityProvider):
		respondAPI(w, 503, map[string]string{"error": "身份服务暂不可用，操作可重试", "code": "IDENTITY_PROVIDER"})
	case errors.Is(err, personnel.ErrNotConfigured):
		respondAPI(w, 503, map[string]string{"error": "本部署未配置账号服务", "code": "ACCOUNT_SERVICE_NOT_CONFIGURED"})
	default:
		h.dbErrors.Add(1)
		respondAPI(w, 503, map[string]string{"error": "service unavailable"})
	}
}

func versionHeader(r *http.Request) (int64, bool) {
	v := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n > 0
}

func (h *apiHandler) registerPersonnel(api *http.ServeMux) {
	ready := func(w http.ResponseWriter, r *http.Request) bool {
		if h.personnel == nil {
			respondAPI(w, 503, map[string]string{"error": "personnel service unavailable"})
			return false
		}
		return true
	}

	// The caller's own system roles (UX: show the 系统管理 entry). Always 200 for a session.
	api.HandleFunc("GET /api/v1/admin/me", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		roles, err := personnel.RolesOf(r.Context(), h.db, currentPrincipal(r).user)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		caps := map[string]bool{}
		for _, c := range []personnel.SystemCapability{personnel.CapPersonnelRead, personnel.CapAccountManage, personnel.CapDepartmentManage,
			personnel.CapSystemRoleManage, personnel.CapPortfolioRoleMgmt, personnel.CapPersonnelAudit} {
			caps[string(c)] = personnel.Has(roles, c)
		}
		respondAPI(w, 200, map[string]any{"userId": currentPrincipal(r).user, "systemRoles": roles, "capabilities": caps, "accountServiceConfigured": h.personnel.IdP != nil})
	})

	api.HandleFunc("GET /api/v1/admin/people", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		out, err := h.personnel.List(r.Context(), h.actor(r), personnel.ListFilter{Query: q.Get("q"), DepartmentID: q.Get("departmentId"), Status: q.Get("status"), SystemRole: q.Get("systemRole"), Limit: limit, Offset: offset})
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})

	api.HandleFunc("POST /api/v1/admin/people", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		var in personnel.ProvisionRequest
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		out, err := h.personnel.ProvisionAccount(r.Context(), h.actor(r), r.Header.Get("Idempotency-Key"), in)
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		status := 201
		if out.Replayed {
			status = 200
		}
		respondAPI(w, status, out)
	})

	api.HandleFunc("GET /api/v1/admin/people/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		out, err := h.personnel.Get(r.Context(), h.actor(r), r.PathValue("id"))
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		w.Header().Set("ETag", `"`+strconv.FormatInt(out.Version, 10)+`"`)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("PATCH /api/v1/admin/people/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		version, ok := versionHeader(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "If-Match version required"})
			return
		}
		var in personnel.Profile
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		out, err := h.personnel.UpdateProfile(r.Context(), h.actor(r), r.PathValue("id"), version, in)
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})

	for _, action := range []string{"disable", "enable"} {
		enabled := action == "enable"
		api.HandleFunc("POST /api/v1/admin/people/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			if !ready(w, r) {
				return
			}
			version, ok := versionHeader(r)
			if !ok {
				respondAPI(w, 428, map[string]string{"error": "If-Match version required"})
				return
			}
			out, err := h.personnel.SetEnabled(r.Context(), h.actor(r), r.PathValue("id"), version, enabled)
			if err != nil {
				h.personnelError(w, r, err)
				return
			}
			respondAPI(w, 200, out)
		})
	}

	api.HandleFunc("POST /api/v1/admin/people/{id}/reset-password", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		out, err := h.personnel.ResetPassword(r.Context(), h.actor(r), r.PathValue("id"))
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})

	api.HandleFunc("POST /api/v1/admin/people/{id}/revoke-sessions", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		out, err := h.personnel.RevokeSessions(r.Context(), h.actor(r), r.PathValue("id"))
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})

	api.HandleFunc("PUT /api/v1/admin/people/{id}/system-roles", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		var in struct {
			Roles         []string `json:"roles"`
			ExpectedRoles []string `json:"expectedRoles"`
		}
		if err := decodeStrict(r, &in); err != nil || in.Roles == nil || in.ExpectedRoles == nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		out, err := h.personnel.SetSystemRoles(r.Context(), h.actor(r), r.PathValue("id"), in.Roles, in.ExpectedRoles)
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})

	api.HandleFunc("PUT /api/v1/admin/people/{id}/portfolio-roles/{scope}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		var in struct {
			Role         *string `json:"role"`
			ExpectedRole *string `json:"expectedRole"`
		}
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		role, expected := "", ""
		if in.Role != nil {
			role = *in.Role
		}
		if in.ExpectedRole != nil {
			expected = *in.ExpectedRole
		}
		out, err := h.personnel.SetPortfolioRole(r.Context(), h.actor(r), r.PathValue("id"), r.PathValue("scope"), role, expected)
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})

	api.HandleFunc("GET /api/v1/admin/departments", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		out, err := h.personnel.Departments(r.Context(), h.actor(r))
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})
	saveDepartment := func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		var in personnel.Department
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if id := r.PathValue("id"); id != "" {
			version, ok := versionHeader(r)
			if !ok || (in.ID != "" && in.ID != id) {
				respondAPI(w, 428, map[string]string{"error": "If-Match version required"})
				return
			}
			in.ID, in.Version = id, version
		} else {
			in.Version = 0
		}
		out, err := h.personnel.SaveDepartment(r.Context(), h.actor(r), in)
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	}
	api.HandleFunc("POST /api/v1/admin/departments", saveDepartment)
	api.HandleFunc("PUT /api/v1/admin/departments/{id}", saveDepartment)

	api.HandleFunc("GET /api/v1/admin/portfolio-scopes", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		out, err := h.personnel.PortfolioScopes(r.Context(), h.actor(r))
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})

	api.HandleFunc("GET /api/v1/admin/provisions", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		out, err := h.personnel.PendingProvisions(r.Context(), h.actor(r))
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})
	api.HandleFunc("POST /api/v1/admin/provisions/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		out, err := h.personnel.ResumeProvision(r.Context(), h.actor(r), r.PathValue("id"))
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})
	api.HandleFunc("POST /api/v1/admin/provisions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w, r) {
			return
		}
		if err := h.personnel.CancelProvision(r.Context(), h.actor(r), r.PathValue("id")); err != nil {
			h.personnelError(w, r, err)
			return
		}
		w.WriteHeader(204)
	})

	// Project-member picker: only for callers who may manage that project's members (portfolio
	// manager of the project's own portfolio). Minimal fields, enabled people only, capped results.
	api.HandleFunc("GET /api/v1/portfolio/projects/{id}/member-candidates", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.formalProjectFor(w, r, identity.CapProjectGrantManage); !ok {
			return
		}
		issuer := ""
		if h.web != nil {
			issuer = h.web.Issuer()
		}
		out, err := personnel.Candidates(r.Context(), h.db, issuer, r.URL.Query().Get("q"), 20)
		if err != nil {
			h.personnelError(w, r, err)
			return
		}
		respondAPI(w, 200, out)
	})
}

// revokeLiveFor adapts the session manager to personnel.Service.RevokeLive.
func (h *apiHandler) revokeLiveFor(ctx context.Context, user string) error {
	if h.leases == nil {
		return nil
	}
	_, err := h.leases.RevokeUser(ctx, user, time.Now())
	return err
}
