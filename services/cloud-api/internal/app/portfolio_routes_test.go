package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPortfolioCoreRoutesRegistered guards against a handler silently missing
// from the runtime: every core portfolio route must resolve to a registered
// pattern (a 404 from the mux itself is a contract break, not a data miss).
func TestPortfolioCoreRoutesRegistered(t *testing.T) {
	h := &apiHandler{}
	api := http.NewServeMux()
	h.registerPortfolio(api)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/portfolio/scopes"},
		{"POST", "/api/v1/portfolio/intakes"},
		{"GET", "/api/v1/portfolio/intakes"},
		{"GET", "/api/v1/portfolio/intakes/abc"},
		{"POST", "/api/v1/portfolio/intakes/abc/transitions"},
		{"POST", "/api/v1/portfolio/intakes/abc/approve"},
		{"POST", "/api/v1/portfolio/resources"},
		{"GET", "/api/v1/portfolio/resources"},
		{"GET", "/api/v1/portfolio/resources/abc"},
		{"GET", "/api/v1/portfolio/resources/abc/calendar"},
		{"PUT", "/api/v1/portfolio/resources/abc/calendar"},
		{"POST", "/api/v1/portfolio/schedule/dependencies"},
		{"GET", "/api/v1/portfolio/schedule/dependencies"},
		{"POST", "/api/v1/portfolio/schedule/items"},
		{"GET", "/api/v1/portfolio/schedule/items"},
		{"GET", "/api/v1/portfolio/schedule/items/abc"},
		{"POST", "/api/v1/portfolio/schedule/items/abc/reservations"},
		{"POST", "/api/v1/portfolio/schedule/approve"},
		{"GET", "/api/v1/portfolio/conflicts"},
		{"GET", "/api/v1/portfolio/conflicts/1"},
		{"POST", "/api/v1/portfolio/conflicts/1/override"},
		{"GET", "/api/v1/portfolio/baselines"},
		{"POST", "/api/v1/portfolio/baselines"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		_, pattern := api.Handler(req)
		if pattern == "" {
			t.Fatalf("core portfolio route not registered: %s %s", tc.method, tc.path)
		}
	}
}
