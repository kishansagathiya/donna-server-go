package schedules

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Service *Service
	Store   *storage.SchedulesStore
	Agents  *storage.AgentsStore
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedules_disabled"})
		return
	}
	var body struct {
		Title          string   `json:"title"`
		Goal           string   `json:"goal"`
		CadenceMinutes *int     `json:"cadence_minutes"`
		SelectedSkills []string `json:"selected_skills"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	sch, err := h.Service.Create(r.Context(), userID, storage.NewScheduleInput{
		Title:          body.Title,
		Goal:           body.Goal,
		CadenceMinutes: body.CadenceMinutes,
		SelectedSkills: body.SelectedSkills,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "disabled") {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"error": "create_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, sch)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedules_disabled"})
		return
	}
	rows, err := h.Store.List(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("status")), queryLimit(r, 50), queryOffset(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedules_disabled"})
		return
	}
	sch, err := h.Store.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "get_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sch)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedules_disabled"})
		return
	}
	var body storage.UpdateScheduleInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	sch, err := h.Store.Update(r.Context(), userID, chi.URLParam(r, "id"), body)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sch)
}

func (h *Handler) Pause(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, storage.ScheduleStatusPaused)
}

func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedules_disabled"})
		return
	}
	id := chi.URLParam(r, "id")
	now := time.Now().UTC().Format(time.RFC3339)
	sch, err := h.Store.Patch(r.Context(), userID, id, map[string]any{
		"status":       storage.ScheduleStatusActive,
		"next_run_at":  now,
		"completed_at": nil,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "resume_failed", "message": err.Error()})
		return
	}
	if h.Service != nil && (sch.CurrentAgentRunID == nil || strings.TrimSpace(*sch.CurrentAgentRunID) == "") {
		_ = h.Service.StartRun(r.Context(), sch)
		sch, _ = h.Store.Get(r.Context(), userID, id)
	}
	writeJSON(w, http.StatusOK, sch)
}

func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, storage.ScheduleStatusArchived)
}

func (h *Handler) RunNow(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Service == nil || h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedules_disabled"})
		return
	}
	id := chi.URLParam(r, "id")
	sch, err := h.Store.Get(r.Context(), userID, id)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "get_failed", "message": err.Error()})
		return
	}
	if sch.Status != storage.ScheduleStatusActive {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "schedule_not_active"})
		return
	}
	if err := h.Service.StartRun(r.Context(), sch); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "run_failed", "message": err.Error()})
		return
	}
	sch, _ = h.Store.Get(r.Context(), userID, id)
	writeJSON(w, http.StatusOK, sch)
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedules_disabled"})
		return
	}
	st := status
	sch, err := h.Store.Update(r.Context(), userID, chi.URLParam(r, "id"), storage.UpdateScheduleInput{Status: &st})
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]string{"error": "status_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sch)
}

func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled || h.Agents == nil || !h.Agents.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedules_disabled"})
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.Store.Get(r.Context(), userID, id); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "get_failed", "message": err.Error()})
		return
	}
	runs, err := h.Agents.ListBySchedule(r.Context(), userID, id, queryLimit(r, 20))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func queryLimit(r *http.Request, defaultLimit int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if n > 200 {
		return 200
	}
	return n
}

func queryOffset(r *http.Request) int {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.Route("/schedules", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Patch("/{id}", h.Update)
		r.Post("/{id}/pause", h.Pause)
		r.Post("/{id}/resume", h.Resume)
		r.Post("/{id}/archive", h.Archive)
		r.Post("/{id}/run", h.RunNow)
		r.Get("/{id}/runs", h.ListRuns)
	})
}
