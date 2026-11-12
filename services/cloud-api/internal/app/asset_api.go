package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/asset"
	"github.com/guolichen007/crane-operations-platform/internal/equipment"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

// authorizeProjectCapability resolves a project grant and checks one explicit
// project capability. No membership in the project hides the object (404); a
// membership without the capability is a 403.
func (h *apiHandler) authorizeProjectCapability(w http.ResponseWriter, r *http.Request, project string, cap identity.Capability) bool {
	if h.web == nil {
		return true
	}
	if project == "" {
		h.deny(w, r, 404, project, "", "project_scope")
		return false
	}
	role, err := identity.ProjectRole(r.Context(), h.db, currentPrincipal(r).user, project)
	if errors.Is(err, identity.ErrDenied) {
		h.deny(w, r, 404, project, "", "project_scope")
		return false
	}
	if err != nil {
		respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
		return false
	}
	if !identity.HasCapability(role, cap) {
		h.deny(w, r, 403, project, "", "role")
		return false
	}
	return true
}

// readableProjects returns the caller's readable project ids, or nil in
// development mode (no project filter).
func (h *apiHandler) readableProjects(r *http.Request) ([]string, error) {
	scopes, err := h.scopes(r)
	if err != nil {
		return nil, err
	}
	if scopes == nil {
		return nil, nil
	}
	projects := make([]string, 0, len(scopes))
	for p := range scopes {
		projects = append(projects, p)
	}
	return projects, nil
}

func parseListOptions(r *http.Request) asset.ListOptions {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	return asset.ListOptions{
		Limit:              limit,
		Offset:             offset,
		Sort:               q.Get("sort"),
		Q:                  q.Get("q"),
		AssetStatus:        q.Get("status"),
		AssetType:          q.Get("type"),
		ManufacturerId:     q.Get("manufacturerId"),
		LocationKind:       q.Get("kind"),
		LocationStatus:     q.Get("status"),
		LocationParent:     q.Get("parentId"),
		EquipmentType:      q.Get("typeCode"),
		EquipmentState:     q.Get("state"),
		Criticality:        q.Get("criticality"),
		CatalogType:        q.Get("typeCode"),
		CatalogStatus:      q.Get("status"),
		CatalogMfg:         q.Get("manufacturerId"),
		ManufacturerStatus: q.Get("status"),
	}
}

func ifMatchVersion(r *http.Request) (int64, bool) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.Trim(v, `"`), 10, 64)
	return n, err == nil && n > 0
}

// requestID threads the validated request id through entity revisions and the
// audit log. It never records credentials, cookies or tokens.
func requestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDKey{}).(string); ok && id != "" {
		return id
	}
	return strings.TrimSpace(r.Header.Get("X-Request-Id"))
}

func decodeStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func setETag(w http.ResponseWriter, version int64) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(version, 10)+`"`)
}

// ---------------------------------------------------------------------------
// Entity -> project resolution. The owning project is always resolved from the
// DB, never trusted from the request body.
// ---------------------------------------------------------------------------
func (h *apiHandler) projectOfAsset(r *http.Request, id string) (string, error) {
	var project string
	err := h.db.QueryRowContext(r.Context(), `SELECT project_id FROM asset_devices WHERE id=$1`, id).Scan(&project)
	return project, err
}

func (h *apiHandler) projectOfLocation(r *http.Request, id string) (string, error) {
	var project string
	err := h.db.QueryRowContext(r.Context(), `SELECT project_id FROM asset_locations WHERE id=$1`, id).Scan(&project)
	return project, err
}

func (h *apiHandler) projectOfEquipment(r *http.Request, id string) (string, error) {
	var project string
	err := h.db.QueryRowContext(r.Context(), `SELECT a.project_id FROM asset_equipment_installations e JOIN asset_devices a ON a.id=e.asset_id WHERE e.id=$1`, id).Scan(&project)
	return project, err
}

func (h *apiHandler) projectOfRelation(r *http.Request, id string) (string, error) {
	var project string
	err := h.db.QueryRowContext(r.Context(), `SELECT a.project_id FROM equipment_relations er JOIN asset_devices a ON a.id=er.asset_id WHERE er.id=$1`, id).Scan(&project)
	return project, err
}

// projectOfManufacturer returns the owning project and whether it is global.
func (h *apiHandler) projectOfManufacturer(r *http.Request, id string) (string, bool, error) {
	var project *string
	err := h.db.QueryRowContext(r.Context(), `SELECT project_id FROM manufacturers WHERE id=$1`, id).Scan(&project)
	if err != nil {
		return "", false, err
	}
	if project == nil {
		return "", true, nil
	}
	return *project, false, nil
}

func (h *apiHandler) projectOfCatalog(r *http.Request, id string) (string, bool, error) {
	var project *string
	err := h.db.QueryRowContext(r.Context(), `SELECT project_id FROM equipment_catalog_items WHERE id=$1`, id).Scan(&project)
	if err != nil {
		return "", false, err
	}
	if project == nil {
		return "", true, nil
	}
	return *project, false, nil
}

// registerAssets wires the B3 business asset master-data routes.
func (h *apiHandler) registerAssets(api *http.ServeMux) {
	// ---- Asset ----
	api.HandleFunc("GET /api/v1/assets", func(w http.ResponseWriter, r *http.Request) {
		projects, err := h.readableProjects(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		// An explicit ?project= narrows the ledger to an authorized project only;
		// an unknown or unauthorized project yields an empty list, never a leak.
		if pf := r.URL.Query().Get("project"); pf != "" {
			if projects == nil {
				projects = []string{pf}
			} else {
				filtered := make([]string, 0, 1)
				for _, p := range projects {
					if p == pf {
						filtered = append(filtered, p)
						break
					}
				}
				projects = filtered
			}
		}
		values, _, err := asset.ListAssetsScoped(r.Context(), h.db, projects, parseListOptions(r))
		if err != nil {
			respondAPI(w, 400, map[string]string{"error": err.Error()})
			return
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/assets", func(w http.ResponseWriter, r *http.Request) {
		var in contract.AssetMasterData
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if !h.authorizeProjectCapability(w, r, in.ProjectId, identity.CapAssetMasterWrite) {
			return
		}
		out, err := asset.CreateAsset(r.Context(), h.db, in, currentPrincipal(r).user, requestID(r), r.Header.Get("Idempotency-Key"))
		if err != nil {
			if errors.Is(err, asset.ErrIdempotencyKey) {
				respondAPI(w, 409, map[string]string{"error": "idempotency conflict"})
				return
			}
			if errors.Is(err, asset.ErrInvalidInput) {
				respondAPI(w, 422, map[string]string{"error": err.Error()})
				return
			}
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 201, out)
	})

	api.HandleFunc("GET /api/v1/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfAsset(r, id)
		if err != nil || !h.authorizeProjectCapability(w, r, project, identity.CapRead) {
			if err != nil {
				respondAPI(w, 404, map[string]string{"error": "not found"})
			}
			return
		}
		out, err := asset.GetAsset(r.Context(), h.db, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("PATCH /api/v1/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfAsset(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapAssetMasterWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		// Presence-aware PATCH: omitted fields keep their stored value, explicit
		// null clears nullable fields, clearing a required field is 422, unknown
		// keys are 400. Never a zero-value whole-object overwrite.
		var patch asset.AssetPatch
		if err := decodeStrict(r, &patch); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		out, err := asset.PatchAsset(r.Context(), h.db, id, patch, expected, currentPrincipal(r).user, requestID(r))
		if err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict):
				respondAPI(w, 409, map[string]string{"error": "version conflict"})
			case errors.Is(err, asset.ErrExpectedVersion), errors.Is(err, asset.ErrRetired), errors.Is(err, asset.ErrTerminalStatus):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			case errors.Is(err, asset.ErrInvalidInput):
				respondAPI(w, 422, map[string]string{"error": err.Error()})
			case errors.Is(err, asset.ErrCrossProject):
				respondAPI(w, 422, map[string]string{"error": "manufacturer belongs to another project"})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("POST /api/v1/assets/{id}/retire", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfAsset(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapAssetMasterWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		if err := asset.RetireAsset(r.Context(), h.db, id, expected, currentPrincipal(r).user, requestID(r)); err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict), errors.Is(err, asset.ErrRetired), errors.Is(err, asset.ErrActiveBinding):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		respondAPI(w, 200, map[string]string{"status": "retired"})
	})

	api.HandleFunc("POST /api/v1/assets/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfAsset(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapAssetMasterWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		var in struct {
			LocationId string `json:"locationId"`
		}
		if err := decodeStrict(r, &in); err != nil || in.LocationId == "" {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if err := asset.MoveAsset(r.Context(), h.db, id, in.LocationId, expected, currentPrincipal(r).user, requestID(r)); err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound), errors.Is(err, asset.ErrCrossProject), errors.Is(err, asset.ErrDisabledLocation):
				respondAPI(w, 404, map[string]string{"error": err.Error()})
			case errors.Is(err, asset.ErrVersionConflict), errors.Is(err, asset.ErrRetired):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		respondAPI(w, 200, map[string]string{"status": "moved"})
	})

	// ---- Location ----
	api.HandleFunc("GET /api/v1/projects/{projectId}/locations", func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if !h.authorizeProjectCapability(w, r, projectID, identity.CapRead) {
			return
		}
		values, _, err := asset.ListLocations(r.Context(), h.db, projectID, parseListOptions(r))
		if err != nil {
			respondAPI(w, 400, map[string]string{"error": err.Error()})
			return
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/projects/{projectId}/locations", func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if !h.authorizeProjectCapability(w, r, projectID, identity.CapLocationWrite) {
			return
		}
		var in contract.AssetLocation
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		in.ProjectId = projectID
		out, err := asset.CreateLocation(r.Context(), h.db, in, currentPrincipal(r).user, requestID(r))
		if err != nil {
			switch {
			case errors.Is(err, asset.ErrInvalidInput):
				respondAPI(w, 422, map[string]string{"error": err.Error()})
			case errors.Is(err, asset.ErrDuplicateLocation):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 201, out)
	})

	api.HandleFunc("GET /api/v1/locations/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfLocation(r, id)
		if err != nil || !h.authorizeProjectCapability(w, r, project, identity.CapRead) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		out, err := asset.GetLocation(r.Context(), h.db, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("PATCH /api/v1/locations/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfLocation(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapLocationWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		var in contract.AssetLocation
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		in.Id = id
		out, err := asset.UpdateLocation(r.Context(), h.db, in, expected, currentPrincipal(r).user, requestID(r))
		if err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict), errors.Is(err, asset.ErrExpectedVersion):
				respondAPI(w, 409, map[string]string{"error": "version conflict"})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("POST /api/v1/locations/{id}/disable", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfLocation(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapLocationWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		if err := asset.DisableLocation(r.Context(), h.db, id, expected, currentPrincipal(r).user, requestID(r)); err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict):
				respondAPI(w, 409, map[string]string{"error": "version conflict"})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		respondAPI(w, 200, map[string]string{"status": "disabled"})
	})

	// ---- Manufacturer ----
	api.HandleFunc("GET /api/v1/manufacturers", func(w http.ResponseWriter, r *http.Request) {
		projects, err := h.readableProjects(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		values, _, err := asset.ListManufacturersScoped(r.Context(), h.db, projects, parseListOptions(r))
		if err != nil {
			respondAPI(w, 400, map[string]string{"error": err.Error()})
			return
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/manufacturers", func(w http.ResponseWriter, r *http.Request) {
		var in contract.ManufacturerInput
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if in.ProjectId == nil || *in.ProjectId == "" {
			respondAPI(w, 403, map[string]string{"error": "global manufacturer is read-only through the business API"})
			return
		}
		if !h.authorizeProjectCapability(w, r, *in.ProjectId, identity.CapCatalogWrite) {
			return
		}
		out, err := asset.CreateManufacturer(r.Context(), h.db, in, currentPrincipal(r).user, requestID(r))
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 201, out)
	})

	api.HandleFunc("GET /api/v1/manufacturers/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, global, err := h.projectOfManufacturer(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !global && !h.authorizeProjectCapability(w, r, project, identity.CapRead) {
			return
		}
		out, err := asset.GetManufacturer(r.Context(), h.db, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("PATCH /api/v1/manufacturers/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, global, err := h.projectOfManufacturer(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if global {
			respondAPI(w, 403, map[string]string{"error": "global manufacturer is read-only through the business API"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapCatalogWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		var in contract.Manufacturer
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		in.Id = id
		out, err := asset.UpdateManufacturer(r.Context(), h.db, in, expected, currentPrincipal(r).user, requestID(r))
		if err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict), errors.Is(err, asset.ErrGlobalReadOnly):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("POST /api/v1/manufacturers/{id}/disable", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, global, err := h.projectOfManufacturer(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if global {
			respondAPI(w, 403, map[string]string{"error": "global manufacturer is read-only through the business API"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapCatalogWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		if err := asset.DisableManufacturer(r.Context(), h.db, id, expected, currentPrincipal(r).user, requestID(r)); err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict):
				respondAPI(w, 409, map[string]string{"error": "version conflict"})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		respondAPI(w, 200, map[string]string{"status": "disabled"})
	})

	// ---- Equipment type dictionary (read-only) ----
	// What the UI may offer when installing Equipment: the code-owned registry
	// (stable code, Chinese name, domain — the same registry that gates spec
	// validation) intersected with the ACTIVE rows of equipment_types (the FK
	// target). A code missing from either side is not installable and not listed.
	api.HandleFunc("GET /api/v1/equipment/types", func(w http.ResponseWriter, r *http.Request) {
		rows, err := h.db.QueryContext(r.Context(), `SELECT type_code, spec_schema_version FROM equipment_types WHERE status='ACTIVE'`)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		defer rows.Close()
		active := map[string]string{}
		for rows.Next() {
			var code, version string
			if err := rows.Scan(&code, &version); err != nil {
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			active[code] = version
		}
		if rows.Err() != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		out := []contract.EquipmentType{}
		for _, t := range equipment.Types() {
			if version, ok := active[t.Code]; ok {
				out = append(out, contract.EquipmentType{TypeCode: t.Code, DisplayName: t.NameZh, Domain: t.Domain, SpecSchemaVersion: version, Status: "ACTIVE"})
			}
		}
		respondAPI(w, 200, out)
	})

	// ---- Equipment Catalog ----
	api.HandleFunc("GET /api/v1/equipment/catalog", func(w http.ResponseWriter, r *http.Request) {
		projects, err := h.readableProjects(r)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "authorization unavailable"})
			return
		}
		values, _, err := asset.ListCatalogItemsScoped(r.Context(), h.db, projects, parseListOptions(r))
		if err != nil {
			respondAPI(w, 400, map[string]string{"error": err.Error()})
			return
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/equipment/catalog", func(w http.ResponseWriter, r *http.Request) {
		var in contract.EquipmentCatalogItem
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if in.ProjectId == nil || *in.ProjectId == "" {
			respondAPI(w, 403, map[string]string{"error": "global catalog is read-only through the business API"})
			return
		}
		if !h.authorizeProjectCapability(w, r, *in.ProjectId, identity.CapCatalogWrite) {
			return
		}
		out, err := asset.CreateCatalogItem(r.Context(), h.db, in, currentPrincipal(r).user, requestID(r))
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 201, out)
	})

	api.HandleFunc("GET /api/v1/equipment/catalog/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, global, err := h.projectOfCatalog(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !global && !h.authorizeProjectCapability(w, r, project, identity.CapRead) {
			return
		}
		out, err := asset.GetCatalogItem(r.Context(), h.db, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("PATCH /api/v1/equipment/catalog/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, global, err := h.projectOfCatalog(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if global {
			respondAPI(w, 403, map[string]string{"error": "global catalog is read-only through the business API"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapCatalogWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		var in contract.EquipmentCatalogItem
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		in.Id = id
		out, err := asset.UpdateCatalogItem(r.Context(), h.db, in, expected, currentPrincipal(r).user, requestID(r))
		if err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict), errors.Is(err, asset.ErrGlobalReadOnly):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("POST /api/v1/equipment/catalog/{id}/disable", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, global, err := h.projectOfCatalog(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if global {
			respondAPI(w, 403, map[string]string{"error": "global catalog is read-only through the business API"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapCatalogWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		if err := asset.DisableCatalogItem(r.Context(), h.db, id, expected, currentPrincipal(r).user, requestID(r)); err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict):
				respondAPI(w, 409, map[string]string{"error": "version conflict"})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		respondAPI(w, 200, map[string]string{"status": "disabled"})
	})

	// ---- Equipment ----
	api.HandleFunc("GET /api/v1/assets/{assetId}/equipment", func(w http.ResponseWriter, r *http.Request) {
		assetID := r.PathValue("assetId")
		project, err := h.projectOfAsset(r, assetID)
		if err != nil || !h.authorizeProjectCapability(w, r, project, identity.CapRead) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		values, _, err := asset.ListEquipment(r.Context(), h.db, assetID, parseListOptions(r))
		if err != nil {
			respondAPI(w, 400, map[string]string{"error": err.Error()})
			return
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/assets/{assetId}/equipment", func(w http.ResponseWriter, r *http.Request) {
		assetID := r.PathValue("assetId")
		project, err := h.projectOfAsset(r, assetID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapAssetMasterWrite) {
			return
		}
		var in struct {
			contract.EquipmentInstallation
			Spec json.RawMessage `json:"spec"`
		}
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		in.AssetId = assetID
		out, err := asset.CreateEquipment(r.Context(), h.db, in.EquipmentInstallation, in.Spec, currentPrincipal(r).user, requestID(r), r.Header.Get("Idempotency-Key"))
		if err != nil {
			if errors.Is(err, asset.ErrIdempotencyKey) {
				respondAPI(w, 409, map[string]string{"error": "idempotency conflict"})
				return
			}
			respondAPI(w, 400, map[string]string{"error": err.Error()})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 201, out)
	})

	api.HandleFunc("GET /api/v1/equipment/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfEquipment(r, id)
		if err != nil || !h.authorizeProjectCapability(w, r, project, identity.CapRead) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		out, err := asset.GetEquipment(r.Context(), h.db, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("PATCH /api/v1/equipment/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfEquipment(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapAssetMasterWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		var in contract.EquipmentInstallation
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		in.Id = id
		out, err := asset.UpdateEquipment(r.Context(), h.db, in, expected, currentPrincipal(r).user, requestID(r))
		if err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict):
				respondAPI(w, 409, map[string]string{"error": "version conflict"})
			case errors.Is(err, asset.ErrRemoved), errors.Is(err, asset.ErrRetired):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 200, out)
	})

	api.HandleFunc("POST /api/v1/equipment/{id}/remove", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfEquipment(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapAssetMasterWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		if err := asset.RemoveEquipment(r.Context(), h.db, id, expected, currentPrincipal(r).user, requestID(r)); err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict), errors.Is(err, asset.ErrActiveBinding), errors.Is(err, asset.ErrRemoved):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		respondAPI(w, 200, map[string]string{"status": "removed"})
	})

	// Runtime binding is DEPLOYMENT_OWNED and deliberately has no business
	// write/read route (no POST/PATCH/DELETE on binding; the deployment owns it).

	// ---- Equipment relations ----
	api.HandleFunc("GET /api/v1/assets/{assetId}/equipment-relations", func(w http.ResponseWriter, r *http.Request) {
		assetID := r.PathValue("assetId")
		project, err := h.projectOfAsset(r, assetID)
		if err != nil || !h.authorizeProjectCapability(w, r, project, identity.CapRead) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		values, _, err := asset.ListRelations(r.Context(), h.db, assetID, parseListOptions(r))
		if err != nil {
			respondAPI(w, 400, map[string]string{"error": err.Error()})
			return
		}
		respondAPI(w, 200, values)
	})

	api.HandleFunc("POST /api/v1/assets/{assetId}/equipment-relations", func(w http.ResponseWriter, r *http.Request) {
		assetID := r.PathValue("assetId")
		project, err := h.projectOfAsset(r, assetID)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapAssetMasterWrite) {
			return
		}
		var in contract.EquipmentRelation
		if err := decodeStrict(r, &in); err != nil {
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		in.AssetId = assetID
		out, err := asset.CreateRelation(r.Context(), h.db, in, currentPrincipal(r).user, requestID(r))
		if err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrRetired), errors.Is(err, asset.ErrRemoved):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 400, map[string]string{"error": err.Error()})
			}
			return
		}
		setETag(w, out.Version)
		respondAPI(w, 201, out)
	})

	api.HandleFunc("POST /api/v1/equipment-relations/{id}/remove", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		project, err := h.projectOfRelation(r, id)
		if err != nil {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapAssetMasterWrite) {
			return
		}
		expected, ok := ifMatchVersion(r)
		if !ok {
			respondAPI(w, 428, map[string]string{"error": "precondition required"})
			return
		}
		if err := asset.RemoveRelation(r.Context(), h.db, id, expected, currentPrincipal(r).user, requestID(r)); err != nil {
			switch {
			case errors.Is(err, asset.ErrNotFound):
				respondAPI(w, 404, map[string]string{"error": "not found"})
			case errors.Is(err, asset.ErrVersionConflict), errors.Is(err, asset.ErrRemoved):
				respondAPI(w, 409, map[string]string{"error": err.Error()})
			default:
				respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			}
			return
		}
		respondAPI(w, 200, map[string]string{"status": "removed"})
	})
}
