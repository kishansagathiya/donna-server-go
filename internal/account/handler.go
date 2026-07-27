package account

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Deleter      *Deleter
	Exporter     *Exporter
	Preferences  *storage.Preferences
	Models       []string
	DefaultModel string
	Personas     []string
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
	persona, personaCustom, _ := h.Preferences.GetPersona(r.Context(), userID)
	persona = normalizePersonaID(persona)
	personaCustom = strings.TrimSpace(personaCustom)
	isCustom := persona == "custom"
	// Only return the custom text when the user is on the custom persona.
	customOut := personaCustom
	if !isCustom {
		customOut = ""
	}
	timezone, _ := h.Preferences.GetTimezone(r.Context(), userID)
	writeJSON(w, http.StatusOK, map[string]any{
		"llm_model":          model,
		"available_models":   h.Models,
		"persona":            persona,
		"persona_custom":     customOut,
		"available_personas": h.Personas,
		"timezone":           timezone,
	})
}

func (h *Handler) updatePreferences(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		LLMModel      string  `json:"llm_model"`
		Persona       string  `json:"persona"`
		PersonaCustom *string `json:"persona_custom"`
		Timezone      *string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}

	// Persona update (optional — only when the persona field is present).
	persona := strings.TrimSpace(body.Persona)
	if persona != "" {
		persona = normalizePersonaID(persona)
		if !h.isAllowedPersona(persona) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":              "invalid_persona",
				"available_personas": h.Personas,
			})
			return
		}
		personaCustom := ""
		if body.PersonaCustom != nil {
			personaCustom = strings.TrimSpace(*body.PersonaCustom)
		}
		if len(personaCustom) > 4000 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "persona_custom_too_long"})
			return
		}
		if err := h.Preferences.SetPersona(r.Context(), userID, persona, personaCustom); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "preferences_failed", "message": err.Error()})
			return
		}
	}

	// Model update (optional — only when llm_model is present).
	body.LLMModel = strings.TrimSpace(body.LLMModel)
	if body.LLMModel != "" {
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
	}

	// Timezone update (optional — pointer so clients can clear with "").
	var timezoneOut string
	if body.Timezone != nil {
		timezoneOut = strings.TrimSpace(*body.Timezone)
		if err := h.Preferences.SetTimezone(r.Context(), userID, timezoneOut); err != nil {
			if strings.Contains(err.Error(), "invalid_timezone") {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_timezone"})
				return
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "preferences_failed", "message": err.Error()})
			return
		}
	}

	resp := map[string]any{}
	if body.LLMModel != "" {
		resp["llm_model"] = body.LLMModel
	}
	if persona != "" {
		resp["persona"] = persona
		if body.PersonaCustom != nil {
			resp["persona_custom"] = strings.TrimSpace(*body.PersonaCustom)
		}
	}
	if body.Timezone != nil {
		resp["timezone"] = timezoneOut
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) isAllowedPersona(persona string) bool {
	for _, allowed := range h.Personas {
		if persona == allowed {
			return true
		}
	}
	return false
}

// normalizePersonaID maps legacy ids onto the current allowlist.
func normalizePersonaID(persona string) string {
	persona = strings.TrimSpace(strings.ToLower(persona))
	if persona == "" {
		return "companion"
	}
	if persona == "therapist" {
		return "listener"
	}
	return persona
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

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}

	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Exporter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "export_unavailable",
			"message": "data export unavailable",
		})
		return
	}

	if err := h.Exporter.PreCheck(userID); err != nil {
		status := http.StatusInternalServerError
		switch {
		case strings.Contains(err.Error(), "rate limited"):
			status = http.StatusTooManyRequests
		case strings.Contains(err.Error(), "unavailable"):
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{
			"error":   "export_failed",
			"message": err.Error(),
		})
		return
	}

	filename := fmt.Sprintf("donna-export-%s.zip", time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	if err := h.Exporter.ExportUser(r.Context(), userID, w); err != nil {
		log.Print("export failed", map[string]any{
			"userId": log.ShortID(userID),
			"error":  err.Error(),
		})
	}
}
