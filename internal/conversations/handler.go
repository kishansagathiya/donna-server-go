package conversations

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Store *storage.Conversations
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "conversations_disabled"})
		return
	}

	q := r.URL.Query()
	opts := storage.ListOptions{
		Limit:           parseLimit(q.Get("limit"), 50),
		IncludeArchived: parseBool(q.Get("include_archived")),
		ArchivedOnly:    parseBool(q.Get("archived_only")),
		Query:           strings.TrimSpace(q.Get("q")),
		Tag:             strings.TrimSpace(q.Get("tag")),
	}

	items, err := h.Store.ListForUser(r.Context(), userID, opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "list_failed",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "conversations_disabled"})
		return
	}

	conversationID := chi.URLParam(r, "id")
	if conversationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_id"})
		return
	}

	detail, err := h.Store.GetWithTurns(r.Context(), userID, conversationID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "load_failed",
			"message": err.Error(),
		})
		return
	}
	if detail == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}

	writeJSON(w, http.StatusOK, detail)
}

type patchRequest struct {
	Title    *string  `json:"title"`
	Archived *bool    `json:"archived"`
	Pinned   *bool    `json:"pinned"`
	Tags     *[]string `json:"tags"`
}

func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "conversations_disabled"})
		return
	}

	conversationID := strings.TrimSpace(chi.URLParam(r, "id"))
	if conversationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_id"})
		return
	}

	var body patchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_body",
			"message": "Expected JSON with title, archived, pinned, and/or tags",
		})
		return
	}
	if body.Title == nil && body.Archived == nil && body.Pinned == nil && body.Tags == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "empty_patch",
			"message": "Provide at least one of title, archived, pinned, tags",
		})
		return
	}

	summary, err := h.Store.UpdateConversation(r.Context(), userID, conversationID, storage.UpdateConversationInput{
		Title:    body.Title,
		Archived: body.Archived,
		Pinned:   body.Pinned,
		Tags:     body.Tags,
	})
	if err != nil {
		status := http.StatusInternalServerError
		code := "update_failed"
		msg := err.Error()
		if msg == "conversation not found" {
			status = http.StatusNotFound
			code = "not_found"
		} else if msg == "title must not be empty" {
			status = http.StatusBadRequest
			code = "invalid_title"
		}
		writeJSON(w, status, map[string]string{"error": code, "message": msg})
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "conversations_disabled"})
		return
	}

	conversationID := strings.TrimSpace(chi.URLParam(r, "id"))
	if conversationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_id"})
		return
	}

	owned, err := h.Store.GetWithTurns(r.Context(), userID, conversationID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "delete_failed",
			"message": err.Error(),
		})
		return
	}
	if owned == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}

	if err := h.Store.DeleteForUser(r.Context(), userID, conversationID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "delete_failed",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": conversationID})
}

func (h *Handler) ListTags(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "conversations_disabled"})
		return
	}

	tags, err := h.Store.ListTags(r.Context(), userID, parseLimit(r.URL.Query().Get("limit"), 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "tags_failed",
			"message": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (h *Handler) TruncateTurns(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "conversations_disabled"})
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "sessionId"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_session_id"})
		return
	}

	fromRaw := r.URL.Query().Get("from_index")
	fromIndex, err := strconv.Atoi(fromRaw)
	if err != nil || fromIndex < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_from_index",
			"message": "from_index must be a non-negative integer",
		})
		return
	}

	if err := h.Store.DeleteTurnsFrom(r.Context(), userID, sessionID, fromIndex); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "truncate_failed",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"from_index": fromIndex,
	})
}

type feedbackRequest struct {
	Rating  string `json:"rating"`
	Comment string `json:"comment,omitempty"`
}

func (h *Handler) UpsertFeedback(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "conversations_disabled"})
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "sessionId"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_session_id"})
		return
	}

	turnIndex, err := strconv.Atoi(chi.URLParam(r, "turnIndex"))
	if err != nil || turnIndex < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_turn_index"})
		return
	}

	var body feedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_body",
			"message": "Expected JSON with rating field",
		})
		return
	}

	if err := h.Store.UpsertTurnFeedback(r.Context(), storage.TurnFeedbackInput{
		UserID:          userID,
		ClientSessionID: sessionID,
		TurnIndex:       turnIndex,
		Rating:          body.Rating,
		Comment:         body.Comment,
	}); err != nil {
		status := http.StatusInternalServerError
		code := "feedback_failed"
		if err.Error() == "conversation not found" {
			status = http.StatusNotFound
			code = "not_found"
		} else if err.Error() == "rating must be up or down" || err.Error() == "invalid turn_index" {
			status = http.StatusBadRequest
			code = "invalid_rating"
		}
		writeJSON(w, status, map[string]string{
			"error":   code,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"rating":     body.Rating,
		"turn_index": turnIndex,
	})
}

func parseLimit(raw string, defaultLimit int) int {
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

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.Route("/conversations", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.List)
		r.Get("/tags", h.ListTags)
		// Static /session routes must be registered before /{id}.
		r.Delete("/session/{sessionId}/turns", h.TruncateTurns)
		r.Put("/session/{sessionId}/turns/{turnIndex}/feedback", h.UpsertFeedback)
		r.Get("/{id}", h.Get)
		r.Patch("/{id}", h.Patch)
		r.Delete("/{id}", h.Delete)
	})
}
