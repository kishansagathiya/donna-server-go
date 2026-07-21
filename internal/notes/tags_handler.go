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
	Items  []storage.NoteTagDetail `json:"items,omitempty"`
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
	details, err := h.Store.GetNoteTagDetails(r.Context(), userID, noteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tags_failed", "message": err.Error()})
		return
	}
	tags := make([]string, 0, len(details))
	for _, d := range details {
		tags = append(tags, d.Tag)
	}
	writeJSON(w, http.StatusOK, noteTagsResponse{NoteID: noteID, Tags: tags, Items: details})
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
	tags, err := h.Store.SetLockedTagsForNote(r.Context(), userID, noteID, body.Tags, "manual")
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
	_ = h.Store.InsertTagCorrection(r.Context(), userID, map[string]any{
		"note_id": noteID,
		"tags":    tags,
		"action":  "manual_set",
	})
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

func (h *Handler) ListTaxonomy(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}
	tags, err := h.Store.ListTaxonomy(r.Context(), userID, queryLimit(r, 100))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "taxonomy_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (h *Handler) PinTag(w http.ResponseWriter, r *http.Request) {
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
		Tag    string `json:"tag"`
		Pinned bool   `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Tag) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	if err := h.Store.PinTag(r.Context(), userID, body.Tag, body.Pinned); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "pin_failed", "message": err.Error()})
		return
	}
	_ = h.Store.InsertTagCorrection(r.Context(), userID, map[string]any{
		"action": "pin",
		"tag":    body.Tag,
		"pinned": body.Pinned,
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "tag": body.Tag, "pinned": body.Pinned})
}

func (h *Handler) AliasTag(w http.ResponseWriter, r *http.Request) {
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
		Source     string `json:"source"`
		Canonical  string `json:"canonical"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.Source) == "" || strings.TrimSpace(body.Canonical) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	if err := h.Store.AliasTag(r.Context(), userID, body.Source, body.Canonical); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "alias_failed", "message": err.Error()})
		return
	}
	_ = h.Store.InsertTagCorrection(r.Context(), userID, map[string]any{
		"action":     "alias",
		"source":     body.Source,
		"canonical":  body.Canonical,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) RenameTag(w http.ResponseWriter, r *http.Request) {
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
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.From) == "" || strings.TrimSpace(body.To) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	if err := h.Store.RenameTag(r.Context(), userID, body.From, body.To); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rename_failed", "message": err.Error()})
		return
	}
	_ = h.Store.InsertTagCorrection(r.Context(), userID, map[string]any{
		"action": "rename",
		"from":   body.From,
		"to":     body.To,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) MergeTags(w http.ResponseWriter, r *http.Request) {
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
		Source    string `json:"source"`
		Canonical string `json:"canonical"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.Source) == "" || strings.TrimSpace(body.Canonical) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	if err := h.Store.MergeTags(r.Context(), userID, body.Source, body.Canonical); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "merge_failed", "message": err.Error()})
		return
	}
	_ = h.Store.InsertTagCorrection(r.Context(), userID, map[string]any{
		"action":     "merge",
		"source":     body.Source,
		"canonical":  body.Canonical,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (h *Handler) ListTagSuggestions(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}
	noteID := strings.TrimSpace(r.URL.Query().Get("note_id"))
	items, err := h.Store.ListPendingTagSuggestions(r.Context(), userID, noteID, queryLimit(r, 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "suggestions_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) ResolveTagSuggestion(w http.ResponseWriter, r *http.Request) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}
	if h.Store == nil || !h.Store.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notes_disabled"})
		return
	}
	suggestionID := chi.URLParam(r, "id")
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	status := strings.TrimSpace(body.Status)
	if status != "accepted" && status != "rejected" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_status"})
		return
	}

	items, err := h.Store.ListPendingTagSuggestions(r.Context(), userID, "", 200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "suggestions_failed", "message": err.Error()})
		return
	}
	var found *storage.MemorySuggestion
	for i := range items {
		if items[i].ID == suggestionID {
			found = &items[i]
			break
		}
	}
	if found == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "suggestion_not_found"})
		return
	}

	if status == "accepted" {
		tag := ""
		if found.Payload != nil {
			if raw, ok := found.Payload["tag"].(string); ok {
				tag = strings.TrimSpace(raw)
			}
		}
		if tag != "" && found.TargetNoteID != nil && *found.TargetNoteID != "" {
			existing, _ := h.Store.GetTagsForNote(r.Context(), userID, *found.TargetNoteID)
			next := append(existing, tag)
			if _, err := h.Store.SetLockedTagsForNote(r.Context(), userID, *found.TargetNoteID, next, "manual"); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "accept_failed", "message": err.Error()})
				return
			}
		}
	}

	if err := h.Store.UpdateSuggestionStatus(r.Context(), userID, suggestionID, status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve_failed", "message": err.Error()})
		return
	}
	_ = h.Store.InsertTagCorrection(r.Context(), userID, map[string]any{
		"action":         status,
		"suggestion_id":  suggestionID,
		"payload":        found.Payload,
		"target_note_id": found.TargetNoteID,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}
