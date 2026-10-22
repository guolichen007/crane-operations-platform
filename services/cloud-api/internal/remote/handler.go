package remote

import (
	"encoding/json"
	"net/http"
)

func Disabled(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "Phase 2 capability disabled"})
}
