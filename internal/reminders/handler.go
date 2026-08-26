package reminders

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
	Store   *storage.RemindersStore
}

type createBody struct {
	Title    string `json:"title"`
	Notes    string `json:"notes"`
	When     string `json:"when"`
	DueAt    string `json:"due_at"`
	Timezone string `json:"timezone"`
}

type updateBody struct {
	Title    *string `json:"title"`
	Notes    *string `json:"notes"`
	When     *string `json:"when"`
	DueAt    *string `json:"due_at"`
	Timezone *string `json:"timezone"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reminders_disabled"})
		return
	}
	var body createBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	in := CreateInput{
		Title:    body.Title,
		Notes:    body.Notes,
		When:     body.When,
		Timezone: body.Timezone,
	}
	if strings.TrimSpace(body.DueAt) != "" {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(body.DueAt)); err == nil {
			in.DueAt = &t
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_due_at", "message": err.Error()})
			return
		}
	}
	rem, err := h.Service.Create(r.Context(), userID, in)
	if err != nil {
		writeJSON(w, statusForErr(err), map[string]string{"error": "create_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, rem)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reminders_disabled"})
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
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reminders_disabled"})
		return
	}
	rem, err := h.Store.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, statusForErr(err), map[string]string{"error": "get_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rem)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled || h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reminders_disabled"})
		return
	}
	existing, err := h.Store.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, statusForErr(err), map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	var body updateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	in := storage.UpdateReminderInput{
		Title: body.Title,
		Notes: body.Notes,
	}
	if body.Timezone != nil {
		in.Timezone = body.Timezone
	}
	if body.DueAt != nil && strings.TrimSpace(*body.DueAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*body.DueAt))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_due_at", "message": err.Error()})
			return
		}
		in.DueAt = &t
	} else if body.When != nil && strings.TrimSpace(*body.When) != "" {
		tzName := existing.Timezone
		if body.Timezone != nil {
			tzName = *body.Timezone
		}
		resolved, err := h.Service.Resolve(r.Context(), userID, CreateInput{
			Title:    existing.Title,
			When:     *body.When,
			Timezone: tzName,
		})
		if err != nil {
			writeJSON(w, statusForErr(err), map[string]string{"error": "update_failed", "message": err.Error()})
			return
		}
		in.DueAt = &resolved.DueAt
		if body.Timezone == nil && resolved.Timezone != "" {
			tz := resolved.Timezone
			in.Timezone = &tz
		}
	}
	rem, err := h.Store.Update(r.Context(), userID, existing.ID, in)
	if err != nil {
		writeJSON(w, statusForErr(err), map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rem)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, storage.ReminderStatusCancelled)
}

func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, storage.ReminderStatusDismissed)
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reminders_disabled"})
		return
	}
	rem, err := h.Store.SetStatus(r.Context(), userID, chi.URLParam(r, "id"), status)
	if err != nil {
		writeJSON(w, statusForErr(err), map[string]string{"error": "status_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rem)
}

func statusForErr(err error) int {
	if err == nil {
		return http.StatusOK
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not_found"):
		return http.StatusNotFound
	case strings.Contains(msg, "disabled"):
		return http.StatusServiceUnavailable
	case strings.HasPrefix(msg, "timezone_required"),
		strings.HasPrefix(msg, "invalid_timezone"),
		strings.HasPrefix(msg, "unparseable_when"):
		return http.StatusBadRequest
	case strings.Contains(msg, "not_editable"),
		strings.Contains(msg, "not_cancellable"),
		strings.Contains(msg, "not_dismissable"),
		strings.Contains(msg, "not_firable"):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
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
	if n > 100 {
		return 100
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.Route("/reminders", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Patch("/{id}", h.Update)
		r.Post("/{id}/cancel", h.Cancel)
		r.Post("/{id}/dismiss", h.Dismiss)
	})
}
