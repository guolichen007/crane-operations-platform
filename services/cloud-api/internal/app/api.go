package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/inventory"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/webauth"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/alarm"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/audit"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/auth"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/device"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/remote"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/session"
)

type apiHandler struct {
	config    platform.Config
	db        *sql.DB
	store     *device.Store
	leases    *session.Manager
	active    func(contract.Shadow) bool
	list      func() []contract.Shadow
	logger    *slog.Logger
	wsClients *atomic.Int64
	dbErrors  *atomic.Int64
	web       *webauth.Manager
}

type principal struct {
	user        string
	meta        webauth.Metadata
	sessionHash []byte
}
type principalKey struct{}

func currentPrincipal(r *http.Request) principal {
	p, _ := r.Context().Value(principalKey{}).(principal)
	return p
}

func respondAPI(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func (h *apiHandler) deny(w http.ResponseWriter, r *http.Request, status int, project, deviceID, reason string) {
	p := currentPrincipal(r)
	if h.web != nil {
		if err := auditlog.Write(r.Context(), h.db, p.meta.Event(p.user, project, deviceID, "authorization_denied", reason)); err != nil {
			status = 503
		}
	}
	respondAPI(w, status, map[string]string{"error": http.StatusText(status)})
}

func (h *apiHandler) authorizeProject(w http.ResponseWriter, r *http.Request, project, deviceID, action string) bool {
	if h.web == nil {
		return true
	}
	role, err := identity.ProjectRole(r.Context(), h.db, currentPrincipal(r).user, project)
	if errors.Is(err, identity.ErrDenied) {
		h.deny(w, r, 404, project, deviceID, "project_scope")
		return false
	}
	if err != nil {
		respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
		return false
	}
	if !identity.Allowed(role, action) {
		h.deny(w, r, 403, project, deviceID, "role")
		return false
	}
	return true
}

func (h *apiHandler) authorizeDevice(w http.ResponseWriter, r *http.Request, id, action string) (string, bool) {
	if !contract.ValidID(id) {
		h.deny(w, r, 404, "", "", "invalid_object")
		return "", false
	}
	if h.web == nil {
		return "", true
	}
	project, err := identity.DeviceProject(r.Context(), h.db, id)
	if errors.Is(err, identity.ErrDenied) {
		h.deny(w, r, 404, "", id, "object_scope")
		return "", false
	}
	if err != nil {
		respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
		return "", false
	}
	return project, h.authorizeProject(w, r, project, id, action)
}

func (h *apiHandler) scopes(r *http.Request) (map[string]bool, error) {
	if h.web == nil {
		return nil, nil
	}
	grants, err := identity.Grants(r.Context(), h.db, currentPrincipal(r).user)
	projects := map[string]bool{}
	for _, grant := range grants {
		if identity.Allowed(grant.Role, "read") {
			projects[grant.ProjectId] = true
		}
	}
	return projects, err
}

func (h *apiHandler) authorizeViewer(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	if h.web == nil {
		return "", "", true
	}
	var deviceID, project, owner string
	err := h.db.QueryRowContext(r.Context(), `SELECT s.device_id,d.project_id,s.user_id FROM live_sessions s
JOIN devices d ON d.id=s.device_id WHERE s.session_id=$1 AND s.expires_at>now()`, r.PathValue("sessionId")).Scan(&deviceID, &project, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		h.deny(w, r, 404, "", "", "viewer_scope")
		return "", "", false
	}
	if err != nil {
		respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
		return "", "", false
	}
	if !h.authorizeProject(w, r, project, deviceID, "watch") {
		return "", "", false
	}
	if owner != currentPrincipal(r).user {
		h.deny(w, r, 404, project, deviceID, "viewer_owner")
		return "", "", false
	}
	return project, deviceID, true
}

func (h *apiHandler) auditContext(r *http.Request, project, deviceID, action string) context.Context {
	if h.web == nil {
		return r.Context()
	}
	p := currentPrincipal(r)
	ctx := identity.WithAuthorization(r.Context(), p.user, p.sessionHash)
	return auditlog.WithEvent(ctx, p.meta.Event(p.user, project, deviceID, action, "success"))
}

func (h *apiHandler) register(mux *http.ServeMux) error {
	if h.config.Env == "production" && h.web == nil {
		return errors.New("production requires initialized BFF")
	}
	if h.web != nil {
		h.web.Register(mux)
	} else {
		mux.HandleFunc("POST /api/v1/auth/dev-session", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := auth.Authenticate(r, h.config.Token); !ok {
				respondAPI(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "crane_dev", Value: h.config.Token, Path: "/api/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 3600})
			respondAPI(w, 200, map[string]string{"mode": "development-only"})
		})
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		scopes, err := h.scopes(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		values, err := inventory.Projects(r.Context(), h.db)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "inventory unavailable"})
			return
		}
		filtered := []contract.InventoryProject{}
		for _, value := range values {
			if scopes == nil || scopes[value.Id] {
				filtered = append(filtered, value)
			}
		}
		respondAPI(w, 200, filtered)
	})
	api.HandleFunc("GET /api/v1/assets/devices", func(w http.ResponseWriter, r *http.Request) {
		scopes, err := h.scopes(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		values, err := inventory.Assets(r.Context(), h.db)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "inventory unavailable"})
			return
		}
		filtered := []contract.AssetDevice{}
		for _, value := range values {
			if scopes == nil || scopes[value.ProjectId] {
				filtered = append(filtered, value)
			}
		}
		respondAPI(w, 200, filtered)
	})
	api.HandleFunc("GET /api/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		allowed := map[string]bool{}
		if h.web != nil {
			rows, err := h.db.QueryContext(r.Context(), `SELECT d.id,m.role FROM devices d JOIN project_memberships m ON m.project_id=d.project_id JOIN users u ON u.id=m.user_id WHERE m.user_id=$1 AND u.enabled`, currentPrincipal(r).user)
			if err != nil {
				respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
				return
			}
			for rows.Next() {
				var id, role string
				if err = rows.Scan(&id, &role); err != nil {
					break
				}
				allowed[id] = identity.Allowed(role, "read")
			}
			err = errors.Join(err, rows.Err(), rows.Close())
			if err != nil {
				respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
				return
			}
		}
		values := []contract.Shadow{}
		for _, value := range h.list() {
			if h.web != nil && !allowed[value.DeviceId] {
				continue
			}
			status, err := h.leases.Status(r.Context(), value.DeviceId)
			if err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			value.UpstreamStatus = status
			values = append(values, value)
		}
		respondAPI(w, 200, values)
	})
	detail := func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := h.authorizeDevice(w, r, id, "read"); !ok {
			return
		}
		value, ok := h.store.Get(id)
		if !ok || !h.active(value) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		status, err := h.leases.Status(r.Context(), id)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		value.UpstreamStatus = status
		respondAPI(w, 200, value)
	}
	api.HandleFunc("GET /api/v1/devices/{id}", detail)
	api.HandleFunc("GET /api/v1/devices/{id}/shadow", detail)
	api.HandleFunc("GET /api/v1/alarms", func(w http.ResponseWriter, r *http.Request) {
		filter := r.URL.Query().Get("deviceId")
		if filter != "" {
			if _, ok := h.authorizeDevice(w, r, filter, "read"); !ok {
				return
			}
		}
		var values []contract.Envelope
		var err error
		if h.web != nil {
			values, err = alarm.ListScoped(r.Context(), h.db, currentPrincipal(r).user, filter)
		} else {
			values, err = alarm.List(r.Context(), h.db)
		}
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, values)
	})
	api.HandleFunc("POST /api/v1/devices/{id}/live-sessions", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, ok := h.authorizeDevice(w, r, id, "watch")
		if !ok {
			return
		}
		if value, ok := h.store.Get(id); !ok || !h.active(value) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		lease, err := h.leases.Open(h.auditContext(r, project, id, "live_start"), platform.ID(), id, currentPrincipal(r).user, time.Now())
		if errors.Is(err, identity.ErrDenied) {
			h.deny(w, r, 404, project, id, "authorization_revoked")
			return
		}
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 201, lease)
	})
	api.HandleFunc("POST /api/v1/live-sessions/{sessionId}/renew", func(w http.ResponseWriter, r *http.Request) {
		project, deviceID, ok := h.authorizeViewer(w, r)
		if !ok {
			return
		}
		lease, err := h.leases.Renew(h.auditContext(r, project, deviceID, "live_renew"), r.PathValue("sessionId"), currentPrincipal(r).user, time.Now())
		if errors.Is(err, identity.ErrDenied) {
			h.deny(w, r, 404, project, deviceID, "authorization_revoked")
			return
		}
		if errors.Is(err, session.ErrNotFound) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respondAPI(w, 200, lease)
	})
	api.HandleFunc("DELETE /api/v1/live-sessions/{sessionId}", func(w http.ResponseWriter, r *http.Request) {
		project, deviceID, ok := h.authorizeViewer(w, r)
		if !ok {
			return
		}
		err := h.leases.Close(h.auditContext(r, project, deviceID, "live_stop"), r.PathValue("sessionId"), currentPrincipal(r).user, time.Now())
		if errors.Is(err, identity.ErrDenied) {
			h.deny(w, r, 404, project, deviceID, "authorization_revoked")
			return
		}
		if errors.Is(err, session.ErrNotFound) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		w.WriteHeader(204)
	})
	api.HandleFunc("POST /api/v1/devices/{id}/remote-sessions", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.authorizeDevice(w, r, r.PathValue("id"), "read"); ok {
			remote.Disabled(w, r)
		}
	})
	api.HandleFunc("DELETE /api/v1/remote-sessions/{sessionId}", remote.Disabled)
	api.HandleFunc("GET /api/v1/devices/{id}/stream", h.stream)
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := api.Handler(r); pattern == "" {
			http.NotFound(w, r)
			return
		}
		if h.web != nil {
			s, err := h.web.Session(r, false)
			p := principal{user: "anonymous", meta: h.web.Metadata(r)}
			if err == nil {
				p.user = s.User.ID
				p.sessionHash = s.Hash
			}
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
			if err != nil {
				status := 503
				if errors.Is(err, webauth.ErrUnauthenticated) {
					status = 401
				}
				h.deny(w, r, status, "", "", "session")
				return
			}
			if stateChanging(r.Method) && !h.web.CheckCSRF(r, s) {
				h.deny(w, r, 403, "", "", "csrf")
				return
			}
			if _, err = h.web.Session(r, r.Method != http.MethodHead); err != nil {
				h.deny(w, r, 401, "", "", "expired_session")
				return
			}
		} else {
			user, ok := auth.Authenticate(r, h.config.Token)
			if !ok {
				respondAPI(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			action := "read"
			if stateChanging(r.Method) {
				action = "watch"
			}
			if strings.Contains(r.URL.Path, "remote-sessions") {
				action = "remote"
			}
			if !auth.Allowed(user.Role, action) {
				respondAPI(w, 403, map[string]string{"error": "forbidden"})
				return
			}
			if stateChanging(r.Method) {
				origin := r.Header.Get("Origin")
				if origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
					respondAPI(w, 403, map[string]string{"error": "origin rejected"})
					return
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					if err := audit.Attempt(ctx, h.db, user.ID, "", r.Method+" "+r.URL.Path); err != nil {
						h.dbErrors.Add(1)
					}
				}()
			}
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{user: user.ID}))
		}
		if stateChanging(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, 16384)
		}
		api.ServeHTTP(w, r)
	})
	if h.web != nil {
		handler = h.web.Transport(handler)
	}
	mux.Handle("/api/", handler)
	return nil
}

func stateChanging(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func (h *apiHandler) stream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := h.authorizeDevice(w, r, id, "read"); !ok {
		return
	}
	if value, ok := h.store.Get(id); !ok || !h.active(value) {
		http.NotFound(w, r)
		return
	}
	if h.web != nil && !h.web.SameOrigin(r, true) {
		h.deny(w, r, 403, "", id, "websocket_origin")
		return
	}
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	h.wsClients.Add(1)
	defer h.wsClients.Add(-1)
	ctx := connection.CloseRead(r.Context())
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if h.web != nil {
			s, err := h.web.Session(r.WithContext(ctx), false)
			project, scopeErr := identity.DeviceProject(ctx, h.db, id)
			if err != nil || scopeErr != nil || identity.Authorize(ctx, h.db, s.User.ID, project, "read") != nil {
				p := currentPrincipal(r)
				_ = auditlog.Write(ctx, h.db, p.meta.Event(p.user, project, id, "authorization_denied", "stream_session_or_scope_expired"))
				_ = connection.Close(websocket.StatusPolicyViolation, "session or scope expired")
				return
			}
		}
		value, _ := h.store.Get(id)
		status, err := h.leases.Status(ctx, id)
		if err != nil {
			return
		}
		value.UpstreamStatus = status
		body, _ := json.Marshal(value)
		writeContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = connection.Write(writeContext, websocket.MessageText, body)
		cancel()
		if err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
