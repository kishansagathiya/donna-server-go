package notes

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// --- Tag DTOs ---

type tagResponse struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

type noteTagsResponse struct {
	NoteID string   `json:"note_id"`
	Tags   []string `json:"tags"`
}

type setTagsRequest struct {
	Tags []string `json:"tags"`
}

// --- Handlers ---

func (h *Handler) ListTags(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	limit := queryLimit(r, 30)
	tags, err := h.Store.ListTopTags(r.Context(), userID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tags_failed", "message": err.Error()})
		return
	}
	out := make([]tagResponse, 0, len(tags))
	for _, t := range tags {
		out = append(out, tagResponse{Tag: t.Name, Count: t.Count})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) GetNoteTags(w http.ResponseWriter, r *http.Request) {
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
	tags, err := h.Store.GetTagsForNote(r.Context(), userID, noteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tags_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, noteTagsResponse{NoteID: noteID, Tags: tags})
}

func (h *Handler) SetNoteTags(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	var body setTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}

	noteID := chi.URLParam(r, "id")
	tags, err := h.Store.SetTagsForNote(r.Context(), userID, noteID, body.Tags)
	if err != nil {
		if strings.Contains(err.Error(), "note not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "note_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "set_tags_failed", "message": err.Error()})
		return
	}
	if tags == nil {
		tags = []string{}
	}
	writeJSON(w, http.StatusOK, noteTagsResponse{NoteID: noteID, Tags: tags})
}

func (h *Handler) NotesForTag(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	tag := strings.TrimSpace(chi.URLParam(r, "tag"))
	if tag == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag_required"})
		return
	}

	noteIDs, err := h.Store.ListNoteIDsForTag(r.Context(), userID, tag)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tag_lookup_failed", "message": err.Error()})
		return
	}
	if len(noteIDs) == 0 {
		writeJSON(w, http.StatusOK, []storage.NoteSummary{})
		return
	}

	// Fetch the matching notes in the same order. PostgREST "in" filter.
	limit := queryLimit(r, 50)
	notes, err := h.Store.GetNotesByIDs(r.Context(), userID, noteIDs, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "notes_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, notes)
}

func (h *Handler) RecomputeTagCounts(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}

	if err := h.Store.RecomputeTagCounts(r.Context(), userID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "recompute_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}