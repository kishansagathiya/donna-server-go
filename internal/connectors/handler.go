package connectors

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
)

// Handler exposes REST routes for integrations.
type Handler struct {
	Service *Service
}

func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.With(authMiddleware).Get("/integrations", h.List)
	r.With(authMiddleware).Post("/integrations/granola/authorize", h.AuthorizeGranola)
	r.With(authMiddleware).Post("/integrations/granola/sync", h.SyncGranola)
	r.With(authMiddleware).Patch("/integrations/granola", h.PatchGranola)
	r.With(authMiddleware).Delete("/integrations/granola", h.DisconnectGranola)
	r.With(authMiddleware).Delete("/integrations/granola/imports", h.DeleteGranolaImports)
	// OAuth callback is unauthenticated (browser redirect) but validates one-time state.
	r.Get("/integrations/granola/callback", h.CallbackGranola)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"integrations": []IntegrationStatus{}})
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	statuses, err := h.Service.ListStatuses(r.Context(), userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"integrations": statuses})
}

type authorizeBody struct {
	ReturnTo string `json:"return_to"`
}

func (h *Handler) AuthorizeGranola(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.ProviderEnabled(ProviderGranola) {
		writeErr(w, http.StatusNotFound, "integrations_disabled", "Granola integration is not enabled")
		return
	}
	var body authorizeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "expected JSON body")
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	result, err := h.Service.StartAuthorize(r.Context(), userID, ProviderGranola, body.ReturnTo)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "integrations_disabled" {
			status = http.StatusNotFound
		}
		writeErr(w, status, "authorize_failed", sanitizeErr(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) CallbackGranola(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		http.Error(w, "integrations disabled", http.StatusNotFound)
		return
	}
	adapter, ok := h.Service.Registry.Get(ProviderGranola)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectAfterOAuth(w, r, ReturnToWeb, false, errParam)
		return
	}
	if code == "" || state == "" {
		h.redirectAfterOAuth(w, r, ReturnToWeb, false, "missing_code_or_state")
		return
	}
	userID, status, err := adapter.HandleCallback(r.Context(), state, code)
	if err != nil {
		h.redirectAfterOAuth(w, r, ReturnToWeb, false, sanitizeErr(err))
		return
	}
	_ = userID
	_ = status
	// return_to is recovered from oauth state inside granola adapter via last successful consume.
	returnTo := r.URL.Query().Get("return_to")
	if g, ok := adapter.(interface{ LastReturnTo() string }); ok && g.LastReturnTo() != "" {
		returnTo = g.LastReturnTo()
	}
	if returnTo == "" {
		returnTo = ReturnToWeb
	}
	h.redirectAfterOAuth(w, r, returnTo, true, "")
	// Kick off initial backfill.
	if userID != "" {
		_ = h.Service.ScheduleSync(r.Context(), userID, ProviderGranola, true)
	}
}

func (h *Handler) SyncGranola(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.ProviderEnabled(ProviderGranola) {
		writeErr(w, http.StatusNotFound, "integrations_disabled", "Granola integration is not enabled")
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	if err := h.Service.ScheduleSync(r.Context(), userID, ProviderGranola, false); err != nil {
		writeErr(w, http.StatusConflict, "sync_failed", sanitizeErr(err))
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "accepted"})
}

type patchBody struct {
	SyncEnabled *bool `json:"sync_enabled"`
}

func (h *Handler) PatchGranola(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.ProviderEnabled(ProviderGranola) {
		writeErr(w, http.StatusNotFound, "integrations_disabled", "Granola integration is not enabled")
		return
	}
	var body patchBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SyncEnabled == nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "sync_enabled required")
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	conn, err := h.Service.Store.GetConnection(r.Context(), userID, ProviderGranola)
	if err != nil || conn == nil {
		writeErr(w, http.StatusNotFound, "not_connected", "no Granola connection")
		return
	}
	if conn.Status == StatusReauthRequired && *body.SyncEnabled {
		writeErr(w, http.StatusConflict, "reauth_required", "reconnect before enabling sync")
		return
	}
	if err := h.Service.Store.PatchConnection(r.Context(), conn.ID, map[string]any{
		"sync_enabled": *body.SyncEnabled,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "patch_failed", "could not update")
		return
	}
	conn, _ = h.Service.Store.GetConnection(r.Context(), userID, ProviderGranola)
	adapter := h.Service.Registry.MustGet(ProviderGranola)
	writeJSON(w, http.StatusOK, adapter.Status(r.Context(), conn))
}

func (h *Handler) DisconnectGranola(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeErr(w, http.StatusNotFound, "integrations_disabled", "integrations are not enabled")
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	adapter := h.Service.Registry.MustGet(ProviderGranola)
	conn, err := h.Service.Store.GetConnection(r.Context(), userID, ProviderGranola)
	if err != nil || conn == nil {
		writeErr(w, http.StatusNotFound, "not_connected", "no Granola connection")
		return
	}
	_ = adapter.Disconnect(r.Context(), *conn)
	conn, _ = h.Service.Store.GetConnection(r.Context(), userID, ProviderGranola)
	st := adapter.Status(r.Context(), conn)
	st.RetainsImportsOnDisconnect = true
	writeJSON(w, http.StatusOK, st)
}

type deleteImportsBody struct {
	Confirm bool `json:"confirm"`
}

func (h *Handler) DeleteGranolaImports(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeErr(w, http.StatusNotFound, "integrations_disabled", "integrations are not enabled")
		return
	}
	var body deleteImportsBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !body.Confirm {
		writeErr(w, http.StatusBadRequest, "confirmation_required", "pass {\"confirm\": true}")
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	adapter := h.Service.Registry.MustGet(ProviderGranola)
	conn, err := h.Service.Store.GetConnection(r.Context(), userID, ProviderGranola)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", "lookup failed")
		return
	}
	// Allow purge even after disconnect: synthesize a minimal connection.
	if conn == nil {
		conn = &Connection{UserID: userID, Provider: ProviderGranola}
	}
	if err := adapter.DeleteImports(r.Context(), *conn); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", sanitizeErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) redirectAfterOAuth(w http.ResponseWriter, r *http.Request, returnTo string, ok bool, errCode string) {
	if returnTo == ReturnToMobile {
		u := "donna://integrations/granola"
		q := url.Values{}
		if ok {
			q.Set("ok", "1")
		} else {
			q.Set("ok", "0")
			if errCode != "" {
				q.Set("error", errCode)
			}
		}
		http.Redirect(w, r, u+"?"+q.Encode(), http.StatusFound)
		return
	}
	base := strings.TrimRight(h.Service.WebAppBase, "/")
	if base == "" {
		base = "/"
	}
	dest := base + "/app?integrations=granola"
	if ok {
		dest += "&ok=1"
	} else {
		dest += "&ok=0&error=" + url.QueryEscape(errCode)
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func sanitizeErr(err error) string {
	if err == nil {
		return "error"
	}
	msg := err.Error()
	// Never leak tokens/secrets in API responses.
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "bearer") {
		if strings.Contains(lower, "refresh") {
			return "token refresh failed"
		}
		if strings.Contains(lower, "expired") {
			return "oauth state expired"
		}
		if strings.Contains(lower, "replay") {
			return "oauth state already used"
		}
		return "authorization failed"
	}
	return msg
}
