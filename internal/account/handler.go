package account

import (
	"encoding/json"
	"net/http"
	"strings"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Deleter      *Deleter
	Preferences  *storage.Preferences
	Models       []string
	DefaultModel string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getPreferences(w, r, userID)
	case http.MethodPatch:
		h.updatePreferences(w, r, userID)
	case http.MethodDelete:
		h.deleteAccount(w, r, userID)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
	}
}

func (h *Handler) getPreferences(w http.ResponseWriter, r *http.Request, userID string) {
	model, err := h.Preferences.GetLLMModel(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "preferences_failed", "message": err.Error()})
		return
	}
	if !h.isAllowedModel(model) {
		model = h.DefaultModel
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"llm_model":        model,
		"available_models": h.Models,
	})
}

func (h *Handler) updatePreferences(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		LLMModel string `json:"llm_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	body.LLMModel = strings.TrimSpace(body.LLMModel)
	if !h.isAllowedModel(body.LLMModel) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":            "invalid_model",
			"available_models": h.Models,
		})
		return
	}
	if err := h.Preferences.SetLLMModel(r.Context(), userID, body.LLMModel); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "preferences_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"llm_model": body.LLMModel})
}

func (h *Handler) isAllowedModel(model string) bool {
	for _, allowed := range h.Models {
		if model == allowed {
			return true
		}
	}
	return false
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request, userID string) {
	if err := h.Deleter.DeleteUser(r.Context(), userID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "delete_failed",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
