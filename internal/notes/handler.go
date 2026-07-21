package notes

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Store *storage.Notes
	Sync  *Sync
	Daily *DailyChecker
	// KB resolves talk-derived notes' audio (source_type = "voice_turn" with
	// a source_id) by signing the conversation-audio user.wav. Nil when
	// persistence is disabled; clients just get a text-only note.
	KB *storage.Knowledge
	// Intents extracts actionable intents after user note updates.
	Intents IntentQueue
	// Flags resolves per-user Notes & Memory V2 feature flags.
	Flags *featureflags.Resolver
}

func (h *Handler) Feed(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}
	if h.Flags != nil {
		flags, err := h.Flags.NotesMemoryV2ForUser(r.Context(), userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "flags_failed", "message": err.Error()})
			return
		}
		if !flags.NotesFeed {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "notes_feed_disabled"})
			return
		}
	}

	curated := true
	if raw := strings.TrimSpace(r.URL.Query().Get("curated")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_curated"})
			return
		}
		curated = parsed
	}

	feed, err := h.Store.ListFeed(r.Context(), userID, storage.NotesFeedQuery{
		Limit:   queryLimit(r, 50),
		Cursor:  strings.TrimSpace(r.URL.Query().Get("cursor")),
		Query:   strings.TrimSpace(r.URL.Query().Get("q")),
		Tag:     strings.TrimSpace(r.URL.Query().Get("tag")),
		Curated: curated,
	})
	if err != nil {
		if strings.Contains(err.Error(), "invalid_cursor") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_cursor"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "feed_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, feed)
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
	// Resolve a playable audio URL: notes-mode dictations store audio in the
	// note-audio bucket (note.audio_path set); talk-derived notes point at a
	// kb_sources row whose conversation/turn we sign in conversation-audio.
	// We try both — one returns a non-empty URL, the other returns "".
	audioURL := h.Store.SignedAudioURL(r.Context(), note)
	if audioURL == "" && note.SourceType == "voice_turn" && h.KB != nil {
		audioURL = h.KB.SignedTalkUserAudioURL(r.Context(), note)
	}
	note.AudioURL = audioURL
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
		ID       string `json:"id"`
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

	note, err := h.Sync.CreateManualWithID(r.Context(), userID, strings.TrimSpace(body.ID), strings.TrimSpace(body.Content), noteDate, nil)
	if err != nil {
		if errors.Is(err, storage.ErrIdempotencyConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":   "idempotency_conflict",
				"message": "A note with this id already exists with different content",
			})
			return
		}
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

	noteID := chi.URLParam(r, "id")
	existing, err := h.Store.GetNoteByID(r.Context(), userID, noteID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if existing.SourceType == "integration" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":   "read_only",
			"message": "Imported integration notes are read-only. Delete them from Integrations.",
		})
		return
	}

	var body struct {
		Content         *string `json:"content"`
		NoteDate        *string `json:"note_date"`
		IsImportant     *bool   `json:"is_important"`
		IsUrgent        *bool   `json:"is_urgent"`
		ContentVersion  *int64  `json:"content_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}

	update := storage.NoteUpdate{
		Content:         body.Content,
		IsImportant:     body.IsImportant,
		IsUrgent:        body.IsUrgent,
		ExpectedVersion: body.ContentVersion,
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

	note, err := h.Store.UpdateNote(r.Context(), userID, noteID, update)
	if err != nil {
		if errors.Is(err, storage.ErrVersionConflict) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":           "content_version_conflict",
				"message":         "Note was modified; reload and retry",
				"content_version": existing.ContentVersion,
			})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	if update.Content != nil {
		if h.Sync != nil {
			h.Sync.enqueueEnrichment(r.Context(), userID, note.ID, note.ContentVersion)
		}
		if h.Intents != nil {
			h.Intents.EnqueueNote(userID, note.ID, note.Content)
		}
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
	if existing, err := h.Store.GetNoteByID(r.Context(), userID, noteID); err == nil && existing.SourceType == "integration" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":   "read_only",
			"message": "Imported integration notes are removed from Integrations → Delete imports.",
		})
		return
	}

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

		r.Get("/feed", h.Feed)
		r.Get("/search", h.Search)
		r.Post("/daily-check", h.DailyCheck)
		r.Get("/recent", h.Recent)
		r.Patch("/{id}", h.Update)
		r.Get("/tags", h.ListTags)
		r.Get("/taxonomy", h.ListTaxonomy)
		r.Post("/tags/pin", h.PinTag)
		r.Post("/tags/alias", h.AliasTag)
		r.Post("/tags/rename", h.RenameTag)
		r.Post("/tags/merge", h.MergeTags)
		r.Get("/tags/{tag}", h.NotesForTag)
		r.Post("/recompute-tags", h.RecomputeTagCounts)

		// Shared CRUD for authenticated web and iOS clients.
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Delete("/{id}", h.Delete)
		r.Get("/{id}/tags", h.GetNoteTags)
		r.Put("/{id}/tags", h.SetNoteTags)

		r.Group(func(r chi.Router) {
			r.Use(RequireWebClient)
			r.Get("/quadrants", h.Quadrants)
		})
	})
}
