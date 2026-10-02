// Package httpapi exposes configuration and acquisition endpoints with chi.
package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"example.com/modbus-instrument-acquisition/internal/config"
	"example.com/modbus-instrument-acquisition/internal/service"
)

const maxBodyBytes = 4 << 20

type Handler struct {
	store *config.Store
	svc   *service.Service
}

func NewRouter(store *config.Store, svc *service.Service) http.Handler {
	h := &Handler{store: store, svc: svc}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/api/v1/config", h.getConfig)
	r.Put("/api/v1/config", h.putConfig)
	r.Post("/api/v1/collect", h.collect)
	r.Post("/api/v1/collect-all", h.collectAll)
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type configEnvelope struct {
	Version int64         `json:"version"`
	Devices []config.Device `json:"devices"`
	Points  []config.Point  `json:"points"`
}

func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request) {
	snap := h.store.Snapshot()
	writeJSON(w, http.StatusOK, configEnvelope{
		Version: snap.Version, Devices: snap.Config.Devices, Points: snap.Config.Points})
}

func (h *Handler) putConfig(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	data, err := io.ReadAll(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	version, err := h.store.Replace(cfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	snap := h.store.Snapshot()
	writeJSON(w, http.StatusOK, configEnvelope{
		Version: version, Devices: snap.Config.Devices, Points: snap.Config.Points})
}

type collectRequest struct {
	Names []string `json:"names"`
}

func (h *Handler) collect(w http.ResponseWriter, r *http.Request) {
	var req collectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if len(req.Names) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "names must not be empty"})
		return
	}
	resp := h.svc.Collect(r.Context(), req.Names)
	h.respondCollect(w, resp)
}

func (h *Handler) collectAll(w http.ResponseWriter, r *http.Request) {
	snap := h.store.Snapshot()
	names := service.SortedNames(snap)
	if len(names) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no points configured"})
		return
	}
	resp := h.svc.Collect(r.Context(), names)
	h.respondCollect(w, resp)
}

func (h *Handler) respondCollect(w http.ResponseWriter, resp service.Response) {
	hasFailure := false
	for _, x := range resp.Results {
		if x.Error != "" {
			hasFailure = true
		}
	}
	if hasFailure {
		writeJSON(w, http.StatusMultiStatus, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
