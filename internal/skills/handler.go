package skills

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Provider *Provider
	Store    *storage.SkillsStore
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Provider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "skills_disabled"})
		return
	}
	all, err := h.Provider.List(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, all)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "skills_disabled"})
		return
	}
	var body struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		Content     string  `json:"content"`
		Raw         string  `json:"raw"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	in := storage.NewSkillInput{
		Name:        body.Name,
		Description: body.Description,
		Content:     body.Content,
		Source:      storage.SkillSourceUser,
	}
	if strings.TrimSpace(body.Raw) != "" {
		parsed, err := ParseSkillMD(body.Raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_skill_md", "message": err.Error()})
			return
		}
		in.Name = parsed.Name
		in.Description = parsed.Description
		in.Content = parsed.Content
	}
	skill := Skill{Name: in.Name, Description: in.Description, Content: in.Content}
	if err := skill.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	row, err := h.Store.Create(r.Context(), userID, in)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "_required") || strings.Contains(err.Error(), "_long") || strings.Contains(err.Error(), "invalid_name") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": "create_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "skills_disabled"})
		return
	}
	row, err := h.Store.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "get_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "skills_disabled"})
		return
	}
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Content     *string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if body.Name != nil {
		probe := Skill{Name: *body.Name}
		if err := probe.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if body.Description != nil && len(*body.Description) > MaxDescriptionLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description_too_long"})
		return
	}
	if body.Content != nil && len(*body.Content) > MaxContentLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content_too_long"})
		return
	}
	row, err := h.Store.Update(r.Context(), userID, chi.URLParam(r, "id"), storage.UpdateSkillInput{
		Name:        body.Name,
		Description: body.Description,
		Content:     body.Content,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "skills_disabled"})
		return
	}
	if err := h.Store.Delete(r.Context(), userID, chi.URLParam(r, "id")); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "delete_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// Export returns the skill rendered as an agentskills.io SKILL.md document.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "skills_disabled"})
		return
	}
	row, err := h.Store.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not_found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "export_failed", "message": err.Error()})
		return
	}
	md := RenderSkillMD(rowToSkill(row))
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="SKILL.md"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(md))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.Route("/skills", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Patch("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
		r.Get("/{id}/export", h.Export)
	})
}
