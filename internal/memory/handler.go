package memory

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
	KB *storage.Knowledge
}

func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.KB == nil || !h.KB.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory_disabled"})
		return
	}

	profile, err := h.KB.GetUserProfileRaw(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "profile_load_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.KB == nil || !h.KB.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory_disabled"})
		return
	}

	var body struct {
		Summary       *string   `json:"summary"`
		IdentityFacts *[]string `json:"identity_facts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	if body.Summary == nil && body.IdentityFacts == nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "no_fields"})
		return
	}

	profile, err := h.KB.UpdateUserProfile(r.Context(), userID, body.Summary, body.IdentityFacts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "profile_update_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) ListFacts(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.KB == nil || !h.KB.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory_disabled"})
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := queryLimit(r, 100)
	offset := queryOffset(r)

	facts, err := h.KB.ListActiveFacts(r.Context(), userID, query, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	if facts == nil {
		facts = []storage.KbFact{}
	}
	writeJSON(w, http.StatusOK, facts)
}

func (h *Handler) CreateFact(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.KB == nil || !h.KB.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory_disabled"})
		return
	}

	var body struct {
		Fact       string  `json:"fact"`
		EntityName *string `json:"entity_name"`
		Topic      *string `json:"topic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	if strings.TrimSpace(body.Fact) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fact_required"})
		return
	}

	fact, err := h.KB.InsertFactReturning(r.Context(), userID, storage.NewFactInput{
		Fact:       strings.TrimSpace(body.Fact),
		EntityName: body.EntityName,
		Topic:      body.Topic,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, fact)
}

func (h *Handler) UpdateFact(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.KB == nil || !h.KB.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory_disabled"})
		return
	}

	var body struct {
		Fact       *string `json:"fact"`
		EntityName *string `json:"entity_name"`
		Topic      *string `json:"topic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	if body.Fact == nil || strings.TrimSpace(*body.Fact) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fact_required"})
		return
	}

	factID := chi.URLParam(r, "id")
	fact, err := h.KB.SupersedeFact(r.Context(), userID, factID, storage.NewFactInput{
		Fact:       strings.TrimSpace(*body.Fact),
		EntityName: body.EntityName,
		Topic:      body.Topic,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "fact_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, fact)
}

func (h *Handler) DeleteFact(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.KB == nil || !h.KB.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory_disabled"})
		return
	}

	factID := chi.URLParam(r, "id")
	if err := h.KB.DeactivateUserFact(r.Context(), userID, factID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "fact_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.Route("/memory", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/profile", h.GetProfile)
		r.Patch("/profile", h.UpdateProfile)
		r.Get("/facts", h.ListFacts)
		r.Post("/facts", h.CreateFact)
		r.Patch("/facts/{id}", h.UpdateFact)
		r.Delete("/facts/{id}", h.DeleteFact)

		// Memory V2 review / grouped UI (#164)
		r.Get("/items", h.ListItems)
		r.Get("/items/grouped", h.ListGrouped)
		r.Get("/items/{id}", h.GetItem)
		r.Patch("/items/{id}", h.UpdateItem)
		r.Delete("/items/{id}", h.DeleteItem)
		r.Post("/items/{id}/accept", h.AcceptItem)
		r.Post("/items/{id}/reject", h.RejectItem)
		r.Post("/items/{id}/outdated", h.MarkOutdatedItem)
		r.Post("/items/{id}/resolve", h.ResolveItem)
		r.Get("/items/{id}/evidence", h.ListEvidence)
		r.Get("/notes/{noteId}/derived", h.ListDerivedFromNote)
		r.Get("/suggestions", h.ListSuggestions)
		r.Post("/suggestions/{id}/accept", h.AcceptSuggestion)
		r.Post("/suggestions/{id}/reject", h.RejectSuggestion)
		r.Post("/suggestions/{id}/resolve", h.ResolveSuggestion)
		r.Post("/feedback", h.PostFeedback)
	})
}
