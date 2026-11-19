package app

// R9 Stage B HTTP surface: first-asset points, meters, maintenance rules / schedules / alerts, work
// orders, replacements, wire-rope inspections, history, and the maintenance & due center.
//
// Authorization is the existing project-scope model: reads need CapRead on the asset's project
// (no membership → 404); work-order execution and point-issue review need CapMaintenanceWrite
// (maintainer, platform_admin); approving a rule, recording a meter baseline and resolving pending
// attributes need CapAssetMasterWrite (platform_admin). SIMULATION / TEST records are always
// returned with their sourceMode; REAL counters never include them.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/metering"
)

func (h *apiHandler) assetFor(w http.ResponseWriter, r *http.Request, cap identity.Capability) (string, string, bool) {
	id := r.PathValue("id")
	project, err := h.projectOfAsset(r, id)
	if err != nil {
		h.deny(w, r, 404, "", "", "asset_scope")
		return "", "", false
	}
	if !h.authorizeProjectCapability(w, r, project, cap) {
		return "", "", false
	}
	return id, project, true
}

func (h *apiHandler) workOrderFor(w http.ResponseWriter, r *http.Request, cap identity.Capability) (metering.WorkOrder, bool) {
	wo, err := metering.GetWorkOrder(r.Context(), h.db, r.PathValue("id"))
	if err != nil {
		h.deny(w, r, 404, "", "", "work_order_scope")
		return wo, false
	}
	return wo, h.authorizeProjectCapability(w, r, wo.ProjectID, cap)
}

func meteringError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metering.ErrNotFound):
		respondAPI(w, 404, map[string]string{"error": "not found"})
	case errors.Is(err, metering.ErrInvalid), errors.Is(err, metering.ErrTransition):
		respondAPI(w, 422, map[string]string{"error": err.Error()})
	case errors.Is(err, metering.ErrConflict):
		respondAPI(w, 409, map[string]string{"error": err.Error()})
	default:
		respondAPI(w, 503, map[string]string{"error": "database unavailable"})
	}
}

func (h *apiHandler) registerMaintenance(api *http.ServeMux) {
	read := func(path string, fn func(w http.ResponseWriter, r *http.Request, asset string)) {
		api.HandleFunc("GET /api/v1/assets/{id}/"+path, func(w http.ResponseWriter, r *http.Request) {
			asset, _, ok := h.assetFor(w, r, identity.CapRead)
			if ok {
				fn(w, r, asset)
			}
		})
	}
	reply := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			meteringError(w, err)
			return
		}
		respondAPI(w, 200, v)
	}
	read("points", func(w http.ResponseWriter, r *http.Request, a string) {
		v, err := metering.Points(r.Context(), h.db, a)
		reply(w, v, err)
	})
	read("point-issues", func(w http.ResponseWriter, r *http.Request, a string) {
		v, err := metering.Issues(r.Context(), h.db, a)
		reply(w, v, err)
	})
	read("pending-attributes", func(w http.ResponseWriter, r *http.Request, a string) {
		v, err := metering.Pending(r.Context(), h.db, a)
		reply(w, v, err)
	})
	read("meters", func(w http.ResponseWriter, r *http.Request, a string) {
		v, err := metering.Meters(r.Context(), h.db, a)
		reply(w, v, err)
	})
	read("maintenance-rules", func(w http.ResponseWriter, r *http.Request, a string) {
		v, err := metering.Rules(r.Context(), h.db, a)
		reply(w, v, err)
	})
	read("maintenance-alerts", func(w http.ResponseWriter, r *http.Request, a string) {
		v, err := metering.Alerts(r.Context(), h.db, []string{a}, r.URL.Query().Get("sourceMode"))
		reply(w, v, err)
	})
	read("work-orders", func(w http.ResponseWriter, r *http.Request, a string) {
		v, err := metering.WorkOrders(r.Context(), h.db, []string{a}, r.URL.Query().Get("sourceMode"), r.URL.Query().Get("status"))
		reply(w, v, err)
	})
	read("maintenance-history", func(w http.ResponseWriter, r *http.Request, a string) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		v, err := metering.History(r.Context(), h.db, a, limit)
		reply(w, v, err)
	})
	api.HandleFunc("GET /api/v1/assets/{id}/meters/{meter}/readings", func(w http.ResponseWriter, r *http.Request) {
		asset, _, ok := h.assetFor(w, r, identity.CapRead)
		if !ok {
			return
		}
		var owner string
		if err := h.db.QueryRowContext(r.Context(), `SELECT asset_id FROM meter_definitions WHERE id=$1`, r.PathValue("meter")).Scan(&owner); err != nil || owner != asset {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		v, err := metering.Readings(r.Context(), h.db, r.PathValue("meter"), limit)
		reply(w, v, err)
	})

	// Review: resolve / accept a point issue (recorded, never deleted).
	api.HandleFunc("POST /api/v1/assets/{id}/point-issues/{issue}/resolve", func(w http.ResponseWriter, r *http.Request) {
		asset, project, ok := h.assetFor(w, r, identity.CapMaintenanceWrite)
		if !ok {
			return
		}
		var in struct {
			Status     string `json:"status"`
			Resolution string `json:"resolution"`
		}
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if err := metering.ResolveIssue(r.Context(), h.db, asset, r.PathValue("issue"), in.Status, strings.TrimSpace(in.Resolution), currentPrincipal(r).user); err != nil {
			meteringError(w, err)
			return
		}
		h.audit(r, project, "point_issue_resolved", in.Status)
		w.WriteHeader(204)
	})

	// Rule approval (platform_admin): a DRAFT real rule becomes effective only with explicit
	// approval, interval and effective date; test-only rules can never be approved (DB CHECK).
	api.HandleFunc("POST /api/v1/assets/{id}/maintenance-rules/{rule}/approve", func(w http.ResponseWriter, r *http.Request) {
		asset, project, ok := h.assetFor(w, r, identity.CapAssetMasterWrite)
		if !ok {
			return
		}
		var in struct {
			IntervalDays  *int      `json:"intervalDays"`
			IntervalMeter *float64  `json:"intervalMeter"`
			EffectiveFrom time.Time `json:"effectiveFrom"`
			Basis         string    `json:"basis"`
		}
		if err := decodeStrict(r, &in); err != nil || in.EffectiveFrom.IsZero() || len(strings.TrimSpace(in.Basis)) < 2 {
			respondAPI(w, 422, map[string]string{"error": "effectiveFrom and basis required"})
			return
		}
		res, err := h.db.ExecContext(r.Context(), `UPDATE maintenance_rules SET approval_status='APPROVED', approved_by=$3, approved_at=now(), effective_from=$4,
interval_days=COALESCE($5,interval_days), interval_meter=COALESCE($6,interval_meter), basis=basis||' ｜ 审批依据：'||$7
WHERE id=$1 AND asset_id=$2 AND approval_status='DRAFT_UNAPPROVED' AND NOT test_only`, r.PathValue("rule"), asset, currentPrincipal(r).user, in.EffectiveFrom, in.IntervalDays, in.IntervalMeter, strings.TrimSpace(in.Basis))
		if err != nil {
			respondAPI(w, 422, map[string]string{"error": "rule cannot be approved with these values (interval required for its type)"})
			return
		}
		if n, _ := res.RowsAffected(); n != 1 {
			respondAPI(w, 409, map[string]string{"error": "rule is not an unapproved real rule"})
			return
		}
		h.audit(r, project, "maintenance_rule_approved", r.PathValue("rule"))
		w.WriteHeader(204)
	})

	// Meter baseline of the slot's current instance (initial usage from the site record).
	api.HandleFunc("POST /api/v1/assets/{id}/meter-baselines", func(w http.ResponseWriter, r *http.Request) {
		asset, project, ok := h.assetFor(w, r, identity.CapAssetMasterWrite)
		if !ok {
			return
		}
		var in struct {
			Slot         string   `json:"equipmentSlot"`
			MeterCode    string   `json:"meterCode"`
			InitialUsage *float64 `json:"initialUsage"`
			Note         string   `json:"note"`
		}
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if err := metering.RecordBaseline(r.Context(), h.db, asset, in.Slot, in.MeterCode, "REAL", in.InitialUsage, currentPrincipal(r).user, in.Note); err != nil {
			meteringError(w, err)
			return
		}
		h.audit(r, project, "meter_baseline_recorded", in.MeterCode)
		w.WriteHeader(204)
	})

	// Alert → work order (one per rule cycle and mode).
	api.HandleFunc("POST /api/v1/maintenance-alerts/{id}/work-order", func(w http.ResponseWriter, r *http.Request) {
		var assetID string
		if err := h.db.QueryRowContext(r.Context(), `SELECT asset_id FROM maintenance_alerts WHERE id=$1`, r.PathValue("id")).Scan(&assetID); err != nil {
			h.deny(w, r, 404, "", "", "alert_scope")
			return
		}
		project, err := h.projectOfAsset(r, assetID)
		if err != nil || !h.authorizeProjectCapability(w, r, project, identity.CapMaintenanceWrite) {
			if err != nil {
				respondAPI(w, 404, map[string]string{"error": "not found"})
			}
			return
		}
		wo, err := metering.CreateFromAlert(r.Context(), h.db, r.PathValue("id"), currentPrincipal(r).user, requestID(r))
		if err != nil {
			meteringError(w, err)
			return
		}
		setETag(w, wo.Version)
		respondAPI(w, 201, wo)
	})
	api.HandleFunc("GET /api/v1/work-orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		wo, ok := h.workOrderFor(w, r, identity.CapRead)
		if ok {
			setETag(w, wo.Version)
			respondAPI(w, 200, wo)
		}
	})
	api.HandleFunc("POST /api/v1/work-orders/{id}/transitions", func(w http.ResponseWriter, r *http.Request) {
		wo, ok := h.workOrderFor(w, r, identity.CapMaintenanceWrite)
		if !ok {
			return
		}
		version, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		var in metering.Transition
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if in.Replacement != nil && wo.SourceMode == "REAL" && !h.hasCapability(r, wo.ProjectID, identity.CapAssetMasterWrite) {
			// A REAL replacement changes the ledger: platform_admin only.
			h.deny(w, r, 403, wo.ProjectID, "", "replacement_ledger")
			return
		}
		out, err := metering.TransitionWorkOrder(r.Context(), h.db, wo.ID, version, in, currentPrincipal(r).user, requestID(r), time.Now())
		if err != nil {
			meteringError(w, err)
			return
		}
		// A completed (or cancelled) order changes the cycle anchor: re-evaluate now so the answer
		// and the next read already show the next cycle (the background loop is the fallback).
		if out.Status == "COMPLETED" || out.Status == "CANCELLED" {
			if _, err := metering.EvaluateAll(r.Context(), h.db, out.AssetID, time.Now()); err != nil {
				select {
				case h.evaluate <- out.AssetID:
				default:
				}
			}
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	// Maintenance & due center: all readable projects, filters applied by the client.
	api.HandleFunc("GET /api/v1/maintenance/center", func(w http.ResponseWriter, r *http.Request) {
		projects, err := h.readableProjects(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		if projects == nil {
			projects = h.allProjects(r)
		}
		rows, err := metering.Center(r.Context(), h.db, projects, time.Now())
		if err != nil {
			meteringError(w, err)
			return
		}
		assets := []string{}
		seen := map[string]bool{}
		for _, x := range rows {
			if !seen[x.AssetID] {
				seen[x.AssetID] = true
				assets = append(assets, x.AssetID)
			}
		}
		alerts, err := metering.Alerts(r.Context(), h.db, assets, "")
		if err != nil {
			meteringError(w, err)
			return
		}
		orders, err := metering.WorkOrders(r.Context(), h.db, assets, "", "")
		if err != nil {
			meteringError(w, err)
			return
		}
		attention, err := metering.Attention(r.Context(), h.db, projects, time.Now())
		if err != nil {
			meteringError(w, err)
			return
		}
		respondAPI(w, 200, map[string]any{"rows": rows, "alerts": alerts, "workOrders": orders, "attention": attention, "generatedAt": time.Now().UTC()})
	})
}

// hasCapability checks a capability without writing a denial.
func (h *apiHandler) hasCapability(r *http.Request, project string, cap identity.Capability) bool {
	if h.web == nil {
		return true
	}
	role, err := identity.ProjectRole(r.Context(), h.db, currentPrincipal(r).user, project)
	return err == nil && identity.HasCapability(role, cap)
}

func (h *apiHandler) allProjects(r *http.Request) []string {
	rows, err := h.db.QueryContext(r.Context(), `SELECT id FROM projects`)
	out := []string{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// audit writes one business audit row for the maintenance surface (no body, no secret).
func (h *apiHandler) audit(r *http.Request, project, action, result string) {
	p := currentPrincipal(r)
	ev := p.meta.Event(p.user, project, "", action, result)
	ev.RequestID = requestIDOr(r, ev.RequestID)
	_ = auditlog.Write(r.Context(), h.db, ev)
}
