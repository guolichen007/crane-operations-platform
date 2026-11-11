package app

// Q3: formal project → Inventory registration (project-scoped).
//
//   POST /api/v1/projects/{projectId}/inventory-registration
//     - requires location_write on THAT project (an actual ProjectGrant: platform_admin today);
//       a portfolio role never substitutes (no portfolio check here at all);
//     - only an approved formal project whose lifecycle is still NULL (first registration);
//     - the administrative code is validated against the embedded geo index and the
//       province/city/county names are derived from it; no coordinates are stored;
//     - update + audit in one transaction; concurrent / repeated requests → exactly one 200,
//       the rest 409, nothing overwritten.
//   GET /api/v1/inventory/pending-projects
//     - the caller's granted, approved, not-yet-registered formal projects (+ whether the
//       caller may register them). Nothing of other users or projects is listed.

import (
	"errors"
	"net/http"
	"strings"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/geo"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/inventory"
)

func (h *apiHandler) registerInventoryRegistration(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/inventory/pending-projects", func(w http.ResponseWriter, r *http.Request) {
		if h.web == nil {
			respondAPI(w, 200, []contract.PendingInventoryProject{})
			return
		}
		rows, err := inventory.PendingFormalProjects(r.Context(), h.db, currentPrincipal(r).user)
		if err != nil {
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		out := make([]contract.PendingInventoryProject, 0, len(rows))
		for _, p := range rows {
			v := contract.PendingInventoryProject{Id: p.ID, Name: p.Name, PortfolioScopeId: p.PortfolioScopeID, Role: p.Role,
				CanRegister: identity.HasCapability(p.Role, identity.CapLocationWrite), AssetCount: p.AssetCount}
			if p.Phase.Valid {
				phase := p.Phase.String
				v.ProjectPhase = &phase
			}
			out = append(out, v)
		}
		respondAPI(w, 200, out)
	})

	api.HandleFunc("POST /api/v1/projects/{projectId}/inventory-registration", func(w http.ResponseWriter, r *http.Request) {
		project := r.PathValue("projectId")
		if !contract.ValidID(project) {
			respondAPI(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !h.authorizeProjectCapability(w, r, project, identity.CapLocationWrite) {
			return
		}
		var in contract.ProjectInventoryRegistration
		if err := decodeStrict(r, &in); err != nil {
			if strings.HasPrefix(err.Error(), "json: unknown field") {
				respondAPI(w, 422, map[string]string{"error": "invalid inventory registration: unknown field"})
				return
			}
			respondAPI(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		ix, err := h.geoIndex()
		if err != nil {
			// Without the administrative index we cannot validate the region: refuse, never guess.
			respondAPI(w, 503, map[string]string{"error": "administrative index unavailable"})
			return
		}
		p := currentPrincipal(r)
		event := p.meta.Event(p.user, project, "", "", "")
		event.RequestID = requestIDOr(r, event.RequestID)
		out, err := inventory.RegisterFormalProject(r.Context(), h.db, project, in, regionResolver(ix), event)
		switch {
		case err == nil:
			respondAPI(w, 200, out)
		case errors.Is(err, inventory.ErrInvalidRegistration):
			respondAPI(w, 422, map[string]string{"error": err.Error()})
		case errors.Is(err, inventory.ErrAlreadyRegistered):
			respondAPI(w, 409, map[string]string{"error": err.Error()})
		case errors.Is(err, inventory.ErrNotFormalProject):
			h.deny(w, r, 404, project, "", "not_formal_project")
		default:
			respondAPI(w, 503, map[string]string{"error": "database unavailable"})
		}
	})
}

// regionResolver derives the province / prefecture / county names of a code from the index.
func regionResolver(ix *geo.Index) inventory.RegionResolver {
	return func(code int) (inventory.Region, bool) {
		r, ok := ix.Region(code)
		if !ok {
			return inventory.Region{}, false
		}
		out := inventory.Region{Level: string(r.Level)}
		for cur, ok := r, true; ok && cur.Code != geo.Country; cur, ok = ix.Region(cur.Parent) {
			switch cur.Level {
			case geo.LevelProvince:
				out.Province = cur.Name
			case geo.LevelPrefecture:
				out.City = cur.Name
			case geo.LevelCounty:
				out.County = cur.Name
			}
		}
		return out, true
	}
}
