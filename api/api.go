// Package api exposes the HTTP interface for config and collection.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"modbus-backend/collect"
	"modbus-backend/config"
)

type Server struct {
	store *config.Store
	col   *collect.Collector
}

func NewServer(store *config.Store, col *collect.Collector) *Server {
	return &Server{store: store, col: col}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/api/config", s.getConfig)
	r.Put("/api/config", s.putConfig)
	r.Post("/api/collect", s.postCollect)
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snapshot())
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var cfg config.Config
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	saved, err := s.store.Replace(cfg)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

type collectRequest struct {
	Names []string `json:"names"`
}

func (s *Server) postCollect(w http.ResponseWriter, r *http.Request) {
	var req collectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Names) == 0 {
		writeErr(w, http.StatusBadRequest, "names must not be empty")
		return
	}
	if len(req.Names) > 1000 {
		writeErr(w, http.StatusBadRequest, "too many names (max 1000)")
		return
	}
	snap := s.store.Snapshot()
	if snap.Version == 0 {
		writeErr(w, http.StatusConflict, "no config uploaded yet")
		return
	}
	res := s.col.Collect(r.Context(), snap, req.Names)
	status := http.StatusOK
	anyOK, anyFail := false, false
	for _, pr := range res.Results {
		if pr.OK {
			anyOK = true
		} else {
			anyFail = true
		}
	}
	if anyFail && !anyOK {
		status = http.StatusBadGateway
	} else if anyFail {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, res)
}
