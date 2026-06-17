package api

import (
	"net/http"
	"time"
)

// health reports API liveness and DB connectivity.
func (a *API) health(w http.ResponseWriter, r *http.Request) {
	db := "ok"
	if err := a.store.Ping(r.Context()); err != nil {
		db = "down"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"db":     db,
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}
