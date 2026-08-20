package agents

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/chat"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Store      *storage.AgentsStore
	Spawner    *Spawner
	Jobs       *storage.BackgroundJobs
	WebAppBase string
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Spawner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}
	var body struct {
		Goal        string                `json:"goal"`
		IntentID    *string               `json:"intent_id"`
		MaxSteps    int                   `json:"max_steps"`
		Skills      []string              `json:"skills"`
		Attachments []chat.ChatAttachment `json:"attachments,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	grounded, err := chat.GroundChatTurn(r.Context(), body.Goal, body.Attachments)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_attachments", "message": err.Error()})
		return
	}
	run, err := h.Spawner.Spawn(r.Context(), userID, SpawnInput{
		Goal:           grounded.DisplayMessage,
		GroundedGoal:   grounded.GroundedMessage,
		IntentID:       body.IntentID,
		MaxSteps:       body.MaxSteps,
		SelectedSkills: body.Skills,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "disabled") {
			status = http.StatusServiceUnavailable
		}
		if strings.Contains(err.Error(), "skill_not_found") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skill_not_found", "message": err.Error()})
			return
		}
		writeJSON(w, status, map[string]string{"error": "spawn_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}
	runs, err := h.Store.List(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("status")), queryLimit(r, 50), queryOffset(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}
	run, err := h.Store.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "get_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) ListSteps(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}
	after := 0
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			after = n
		}
	}
	steps, err := h.Store.ListSteps(r.Context(), userID, chi.URLParam(r, "id"), after, queryLimit(r, 200))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, steps)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}
	run, err := h.Store.Cancel(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "cancel_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) Finish(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}
	run, err := h.Store.MarkFinished(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "finish_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}
	var body struct {
		Message     string                `json:"message"`
		Attachments []chat.ChatAttachment `json:"attachments,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	runID := chi.URLParam(r, "id")
	grounded, err := chat.GroundChatTurn(r.Context(), body.Message, body.Attachments)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_attachments", "message": err.Error()})
		return
	}
	msg := strings.TrimSpace(grounded.GroundedMessage)
	run, err := h.Store.SetRedirect(r.Context(), userID, runID, msg)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "redirect_failed", "message": err.Error()})
		return
	}
	// Resume paused/finished runs so the reply is consumed by the harness.
	switch run.Status {
	case storage.AgentStatusWaitingForUser, storage.AgentStatusSucceeded, storage.AgentStatusFailed, storage.AgentStatusQueued:
		run, err = ResumeAfterApproval(r.Context(), h.Store, h.Jobs, userID, runID, "")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resume_failed", "message": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, run)
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
	if n > 500 {
		return 500
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
	r.Get("/share/agent/{token}", h.GetPublicShare)

	r.Route("/agent-runs", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}/share", h.GetShare)
		r.Post("/{id}/share", h.CreateShare)
		r.Delete("/{id}/share", h.RevokeShare)
		r.Get("/{id}", h.Get)
		r.Get("/{id}/steps", h.ListSteps)
		r.Post("/{id}/cancel", h.Cancel)
		r.Post("/{id}/finish", h.Finish)
		r.Post("/{id}/redirect", h.Redirect)
	})
}
