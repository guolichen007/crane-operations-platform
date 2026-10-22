// Remote entry is deliberately fail-closed until Phase 2 identity/session review.
package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "remote access disabled: Phase 2", http.StatusForbidden)
	})
	s := &http.Server{Addr: ":8082", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(s.ListenAndServe())
}
