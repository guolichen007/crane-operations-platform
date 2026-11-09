package app

// GET /api/v1/operations/summary (Homepage H2-1C). Read-only statistics over the caller's
// authorized Inventory projects. 200 complete summary; 400 malformed region / alarmWindow /
// unknown parameter; 401 no session (enforced by the shared /api/ handler); 404 region not in the
// administrative index; 503 index, authorization or statistics query failed — never partial data.

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/geo"
	"github.com/guolichen007/crane-operations-platform/internal/operations"
)

// summaryTimeout bounds the single statement. It is applied through the request context
// (pgx cancels the backend query), not via SET statement_timeout, so no session-level setting
// can survive on a pooled connection.
const summaryTimeout = 5 * time.Second

var regionParam = regexp.MustCompile(`^[0-9]{6}$`)

func (h *apiHandler) registerOperations(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/operations/summary", func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		q := r.URL.Query()
		for key := range q {
			if key != "region" && key != "alarmWindow" {
				h.opsLog(r, started, "", "bad_request", 400)
				respondAPI(w, 400, map[string]string{"error": "bad_request"})
				return
			}
		}
		req := operations.Request{User: currentPrincipal(r).user, AllProjects: h.web == nil, Window: "PT24H"}
		if len(q["alarmWindow"]) > 1 || len(q["region"]) > 1 {
			h.opsLog(r, started, "", "bad_request", 400)
			respondAPI(w, 400, map[string]string{"error": "bad_request"})
			return
		}
		if v, ok := q["alarmWindow"]; ok {
			if _, valid := operations.Windows[v[0]]; !valid {
				h.opsLog(r, started, "", "bad_request", 400)
				respondAPI(w, 400, map[string]string{"error": "bad_request"})
				return
			}
			req.Window = v[0]
		}
		scope := "country"
		if v, ok := q["region"]; ok {
			if !regionParam.MatchString(v[0]) {
				h.opsLog(r, started, "", "bad_request", 400)
				respondAPI(w, 400, map[string]string{"error": "bad_request"})
				return
			}
			code, _ := strconv.Atoi(v[0])
			if code != geo.Country {
				req.Region = &code
				scope = v[0]
			}
		}
		ix, err := h.geoIndex()
		if err != nil {
			h.opsLog(r, started, scope, "geo_unavailable", 503)
			respondAPI(w, 503, map[string]string{"error": "summary_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), summaryTimeout)
		defer cancel()
		summary, err := operations.Summary(ctx, h.db, ix, h.ops, req, time.Now)
		switch {
		case err == nil:
			h.opsLog(r, started, scope, "", 200)
			respondAPI(w, 200, summary)
		case errors.Is(err, operations.ErrRegionNotFound):
			h.opsLog(r, started, scope, "region_not_found", 404)
			respondAPI(w, 404, map[string]string{"error": "region_not_found"})
		case errors.Is(err, operations.ErrInvalidWindow):
			h.opsLog(r, started, scope, "bad_request", 400)
			respondAPI(w, 400, map[string]string{"error": "bad_request"})
		default:
			kind := "query_failed"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				kind = "timeout"
			} else if r.Context().Err() != nil {
				kind = "client_cancelled"
			}
			h.opsLog(r, started, scope, kind, 503)
			h.logger.Error("operations summary failed", "event", "operations_summary_error", "requestId", requestID(r), "error", err.Error())
			respondAPI(w, 503, map[string]string{"error": "summary_unavailable"})
		}
	})
}

// opsLog: structured, no token, no device detail — duration, outcome, scope and request id only.
func (h *apiHandler) opsLog(r *http.Request, started time.Time, scope, errorKind string, status int) {
	h.logger.Info("operations summary", "event", "operations_summary", "requestId", requestID(r), "status", status,
		"durationMs", time.Since(started).Milliseconds(), "scope", scope, "errorKind", errorKind, "userId", currentPrincipal(r).user)
}

func (h *apiHandler) geoIndex() (*geo.Index, error) {
	if h.geo != nil {
		return h.geo()
	}
	return geo.Default()
}
