package notes

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
	Store  *storage.Notes
	Sync   *Sync
	Daily  *DailyChecker
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q_required"})
		return
	}

	limit := queryLimit(r, 20)
	notes, err := h.Store.SearchNotes(r.Context(), userID, q, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "search_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, notes)
}

func (h *Handler) Recent(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	limit := queryLimit(r, 50)
	offset := queryOffset(r)
	notes, err := h.Store.ListRecent(r.Context(), userID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, notes)
}

func (h *Handler) DailyCheck(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Daily == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	briefing, err := h.Daily.Check(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "daily_check_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, briefing)
}

func (h *Handler) Quadrants(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	ctx := r.Context()
	doFirst, _ := h.Store.ListQuadrant(ctx, userID, true, true, 10)
	schedule, _ := h.Store.ListQuadrant(ctx, userID, false, true, 10)
	delegate, _ := h.Store.ListQuadrant(ctx, userID, true, false, 10)
	eliminate, _ := h.Store.ListQuadrant(ctx, userID, false, false, 10)

	writeJSON(w, http.StatusOK, map[string]any{
		"doFirst":   doFirst,
		"schedule":  schedule,
		"delegate":  delegate,
		"eliminate": eliminate,
	})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	noteID := chi.URLParam(r, "id")
	note, err := h.Store.GetNoteByID(r.Context(), userID, noteID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "note_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Sync == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	var body struct {
		Content  string `json:"content"`
		NoteDate string `json:"note_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "content_required"})
		return
	}

	var noteDate *time.Time
	if strings.TrimSpace(body.NoteDate) != "" {
		parsed, err := time.Parse(time.RFC3339, body.NoteDate)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_note_date"})
			return
		}
		noteDate = &parsed
	}

	note, err := h.Sync.CreateManual(r.Context(), userID, strings.TrimSpace(body.Content), noteDate)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	var body struct {
		Content     *string `json:"content"`
		NoteDate    *string `json:"note_date"`
		IsImportant *bool   `json:"is_important"`
		IsUrgent    *bool   `json:"is_urgent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}

	update := storage.NoteUpdate{
		Content:     body.Content,
		IsImportant: body.IsImportant,
		IsUrgent:    body.IsUrgent,
	}
	if body.NoteDate != nil && strings.TrimSpace(*body.NoteDate) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*body.NoteDate))
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_note_date"})
			return
		}
		formatted := parsed.UTC().Format(time.RFC3339)
		update.NoteDate = &formatted
	}

	noteID := chi.URLParam(r, "id")
	note, err := h.Store.UpdateNote(r.Context(), userID, noteID, update)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	noteID := chi.URLParam(r, "id")
	if err := h.Store.DeleteNote(r.Context(), userID, noteID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "delete_failed", "message": err.Error()})
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

func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.Route("/notes", func(r chi.Router) {
		r.Use(authMiddleware)

		r.Get("/search", h.Search)

		r.Group(func(r chi.Router) {
			r.Use(RequireWebClient)
			r.Get("/recent", h.Recent)
			r.Get("/quadrants", h.Quadrants)
			r.Post("/daily-check", h.DailyCheck)
			r.Post("/", h.Create)
			r.Get("/{id}", h.Get)
			r.Patch("/{id}", h.Update)
			r.Delete("/{id}", h.Delete)
		})
	})
}
