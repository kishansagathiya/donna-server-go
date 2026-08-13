package agents

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
)

type shareResponse struct {
	URL       string  `json:"url"`
	Token     string  `json:"token"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

func (h *Handler) shareURL(token string) string {
	base := strings.TrimRight(strings.TrimSpace(h.WebAppBase), "/")
	if base == "" {
		base = "https://donnadoesit.com"
	}
	return base + "/share/agent/" + token
}

func (h *Handler) GetShare(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}

	runID := strings.TrimSpace(chi.URLParam(r, "id"))
	if runID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_id"})
		return
	}

	share, err := h.Store.GetShareForUser(r.Context(), userID, runID)
	if err != nil {
		status := http.StatusInternalServerError
		code := "share_failed"
		if err.Error() == "agent run not found" {
			status = http.StatusNotFound
			code = "not_found"
		}
		writeJSON(w, status, map[string]string{"error": code, "message": err.Error()})
		return
	}
	if share == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}

	writeJSON(w, http.StatusOK, shareResponse{
		URL:       h.shareURL(share.Token),
		Token:     share.Token,
		CreatedAt: share.CreatedAt,
		ExpiresAt: share.ExpiresAt,
	})
}

func (h *Handler) CreateShare(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}

	runID := strings.TrimSpace(chi.URLParam(r, "id"))
	if runID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_id"})
		return
	}

	share, err := h.Store.CreateShare(r.Context(), userID, runID)
	if err != nil {
		status := http.StatusInternalServerError
		code := "share_failed"
		if err.Error() == "agent run not found" {
			status = http.StatusNotFound
			code = "not_found"
		}
		writeJSON(w, status, map[string]string{"error": code, "message": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, shareResponse{
		URL:       h.shareURL(share.Token),
		Token:     share.Token,
		CreatedAt: share.CreatedAt,
		ExpiresAt: share.ExpiresAt,
	})
}

func (h *Handler) RevokeShare(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}

	runID := strings.TrimSpace(chi.URLParam(r, "id"))
	if runID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_id"})
		return
	}

	if err := h.Store.RevokeShare(r.Context(), userID, runID); err != nil {
		status := http.StatusInternalServerError
		code := "revoke_failed"
		if err.Error() == "agent run not found" {
			status = http.StatusNotFound
			code = "not_found"
		}
		writeJSON(w, status, map[string]string{"error": code, "message": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GetPublicShare serves a shared agent run without authentication.
func (h *Handler) GetPublicShare(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents_disabled"})
		return
	}

	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_token"})
		return
	}

	detail, err := h.Store.GetPublicByShareToken(r.Context(), token)
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
