package actions

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Store    *storage.ActionsStore
	Executor *Executor
}

type IntentWithRun struct {
	storage.Intent
	Run *storage.ActionRun `json:"run,omitempty"`
}

func (h *Handler) ListIntents(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "actions_disabled"})
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "open"
	}
	intents, err := h.Store.ListIntents(r.Context(), userID, status, queryLimit(r, 50), queryOffset(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}

	out := make([]IntentWithRun, 0, len(intents))
	for _, intent := range intents {
		item := IntentWithRun{Intent: intent}
		if run, err := h.Store.FindActiveRunForIntent(r.Context(), userID, intent.ID); err == nil {
			enriched := h.enrichRun(r.Context(), run)
			item.Run = &enriched
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) DismissIntent(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "actions_disabled"})
		return
	}

	intentID := chi.URLParam(r, "id")
	intent, err := h.Store.DismissIntent(r.Context(), userID, intentID)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "dismiss_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "actions_disabled"})
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	runs, err := h.Store.ListActionRuns(r.Context(), userID, status, queryLimit(r, 50), queryOffset(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	out := make([]storage.ActionRun, 0, len(runs))
	for _, run := range runs {
		out = append(out, h.enrichRun(r.Context(), run))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) ConfirmRun(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Executor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "actions_disabled"})
		return
	}

	runID := chi.URLParam(r, "id")
	run, err := h.Executor.ConfirmAndExecute(r.Context(), userID, runID)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "confirm_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h.enrichRun(r.Context(), run))
}

func (h *Handler) CancelRun(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Executor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "actions_disabled"})
		return
	}

	runID := chi.URLParam(r, "id")
	run, err := h.Executor.Cancel(r.Context(), userID, runID)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "cancel_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h.enrichRun(r.Context(), run))
}

func (h *Handler) enrichRun(ctx context.Context, run storage.ActionRun) storage.ActionRun {
	if h.Store == nil {
		return run
	}
	action, err := h.Store.GetActionByID(ctx, run.ActionID)
	if err != nil {
		return run
	}
	slug := action.Slug
	name := action.Name
	risk := action.Risk
	run.ActionSlug = &slug
	run.ActionName = &name
	run.ActionRisk = &risk
	return run
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
	r.Route("/intents", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.ListIntents)
		r.Post("/{id}/dismiss", h.DismissIntent)
	})
	r.Route("/action-runs", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.ListRuns)
		r.Post("/{id}/confirm", h.ConfirmRun)
		r.Post("/{id}/cancel", h.CancelRun)
	})
}
