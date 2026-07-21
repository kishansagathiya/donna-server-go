package memory

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// ListItems lists memories by status/kind for the review inbox and Memory UI.
// GET /memory/items?status=active|pending|sensitive|conflicting|rejected|outdated&kind=&q=
func (h *Handler) ListItems(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}

	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	if status == "" {
		status = "active"
	}

	// Conflicting candidates are pending memory suggestions with payload.conflicting.
	if status == "conflicting" {
		items, err := h.listConflictingSuggestions(r, userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, items)
		return
	}

	facts, err := h.KB.ListMemoryFactsFiltered(r.Context(), userID, storage.MemoryListFilter{
		Status: status,
		Kind:   r.URL.Query().Get("kind"),
		Query:  r.URL.Query().Get("q"),
		Limit:  queryLimit(r, 100),
		Offset: queryOffset(r),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}

	// Pending inbox also includes unresolved memory suggestions (not yet facts).
	if status == "pending" || status == "sensitive" {
		suggestions, err := h.KB.ListMemorySuggestionsFiltered(r.Context(), userID, storage.SuggestionPending, 100)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "suggestions_failed", "message": err.Error()})
			return
		}
		items := factsToItems(facts)
		for _, s := range suggestions {
			item, include := suggestionToItem(s, status == "sensitive")
			if include {
				items = append(items, item)
			}
		}
		writeJSON(w, http.StatusOK, items)
		return
	}

	writeJSON(w, http.StatusOK, factsToItems(facts))
}

// ListGrouped returns active memories bucketed for the Memory UI.
// GET /memory/items/grouped
func (h *Handler) ListGrouped(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}

	facts, err := h.KB.ListMemoryFactsFiltered(r.Context(), userID, storage.MemoryListFilter{
		Status: "active",
		Query:  r.URL.Query().Get("q"),
		Limit:  queryLimit(r, 200),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}

	buckets := map[string][]storage.MemoryItem{}
	for _, kind := range storage.MemoryUIGroupOrder {
		buckets[kind] = []storage.MemoryItem{}
	}
	other := []storage.MemoryItem{}

	for _, f := range facts {
		kind := ""
		if f.MemoryKind != nil {
			kind = *f.MemoryKind
		}
		group := storage.NormalizeMemoryKindForUI(kind)
		item := storage.MemoryItem{MemoryFact: f}
		if group == storage.MemoryKindOther || buckets[group] == nil {
			other = append(other, item)
			continue
		}
		buckets[group] = append(buckets[group], item)
	}

	groups := make([]storage.MemoryGroup, 0, len(storage.MemoryUIGroupOrder)+1)
	for _, kind := range storage.MemoryUIGroupOrder {
		groups = append(groups, storage.MemoryGroup{
			Kind:  kind,
			Label: memoryKindLabel(kind),
			Items: buckets[kind],
		})
	}
	if len(other) > 0 {
		groups = append(groups, storage.MemoryGroup{
			Kind:  storage.MemoryKindOther,
			Label: "Other",
			Items: other,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// GetItem returns one memory with evidence.
// GET /memory/items/{id}
func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	factID := chi.URLParam(r, "id")
	fact, err := h.KB.GetMemoryFactByID(r.Context(), userID, factID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "fact_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed", "message": err.Error()})
		return
	}
	evidence, err := h.KB.ListMemoryEvidence(r.Context(), userID, factID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "evidence_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, storage.MemoryItem{MemoryFact: fact, Evidence: evidence})
}

// UpdateItem edits a memory (supersede-style rewrite of text/kind fields).
// PATCH /memory/items/{id}
func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	factID := chi.URLParam(r, "id")
	var body struct {
		Fact         *string        `json:"fact"`
		EntityName   *string        `json:"entity_name"`
		Topic        *string        `json:"topic"`
		MemoryKind   *string        `json:"memory_kind"`
		Predicate    *string        `json:"predicate"`
		ObjectValue  map[string]any `json:"object_value"`
		Sensitivity  *string        `json:"sensitivity"`
		ReviewStatus *string        `json:"review_status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}

	existing, err := h.KB.GetMemoryFactByID(r.Context(), userID, factID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "fact_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed", "message": err.Error()})
		return
	}

	// Text edits create a superseding active fact (preserves provenance chain).
	if body.Fact != nil && strings.TrimSpace(*body.Fact) != "" && strings.TrimSpace(*body.Fact) != existing.Fact {
		in := storage.MemoryFactInput{
			Fact:         strings.TrimSpace(*body.Fact),
			EntityName:   coalescePtr(body.EntityName, existing.EntityName),
			Topic:        coalescePtr(body.Topic, existing.Topic),
			SourceID:     existing.SourceID,
			SupersedesID: &existing.ID,
			MemoryKind:   firstNonEmptyStr(ptrStr(body.MemoryKind), ptrStr(existing.MemoryKind), storage.MemoryKindFact),
			Predicate:    firstNonEmptyStr(ptrStr(body.Predicate), ptrStr(existing.Predicate)),
			ObjectValue:  body.ObjectValue,
			Confidence:   1,
			Sensitivity:  firstNonEmptyStr(ptrStr(body.Sensitivity), existing.Sensitivity, storage.SensitivityNormal),
			ReviewStatus: storage.ReviewActive,
		}
		if in.ObjectValue == nil {
			in.ObjectValue = existing.ObjectValue
		}
		if err := h.KB.MarkMemoryFactSuperseded(r.Context(), userID, existing.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update_failed", "message": err.Error()})
			return
		}
		fact, err := h.KB.InsertMemoryFact(r.Context(), userID, in)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update_failed", "message": err.Error()})
			return
		}
		_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, &fact.ID, nil, storage.FeedbackEdit, map[string]any{
			"previous_fact_id": existing.ID,
			"previous_fact":    existing.Fact,
		})
		_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
		writeJSON(w, http.StatusOK, storage.MemoryItem{MemoryFact: fact})
		return
	}

	patch := map[string]any{}
	if body.EntityName != nil {
		patch["entity_name"] = strings.TrimSpace(*body.EntityName)
	}
	if body.Topic != nil {
		patch["topic"] = strings.TrimSpace(*body.Topic)
	}
	if body.MemoryKind != nil {
		patch["memory_kind"] = strings.TrimSpace(*body.MemoryKind)
	}
	if body.Predicate != nil {
		patch["predicate"] = strings.TrimSpace(*body.Predicate)
	}
	if body.ObjectValue != nil {
		patch["object_value"] = body.ObjectValue
	}
	if body.Sensitivity != nil {
		patch["sensitivity"] = strings.TrimSpace(*body.Sensitivity)
	}
	if body.ReviewStatus != nil {
		status := strings.TrimSpace(*body.ReviewStatus)
		patch["review_status"] = status
		patch["active"] = status == storage.ReviewActive
	}
	fact, err := h.KB.UpdateMemoryFactFields(r.Context(), userID, factID, patch)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, storage.MemoryItem{MemoryFact: fact})
}

// AcceptItem activates a pending_review fact (sensitive path enforced).
// POST /memory/items/{id}/accept
func (h *Handler) AcceptItem(w http.ResponseWriter, r *http.Request) {
	h.mutateReviewStatus(w, r, storage.ReviewActive, storage.FeedbackAccept)
}

// RejectItem rejects a pending or active memory.
// POST /memory/items/{id}/reject
func (h *Handler) RejectItem(w http.ResponseWriter, r *http.Request) {
	h.mutateReviewStatus(w, r, storage.ReviewRejected, storage.FeedbackReject)
}

// MarkOutdatedItem marks a memory outdated.
// POST /memory/items/{id}/outdated
func (h *Handler) MarkOutdatedItem(w http.ResponseWriter, r *http.Request) {
	h.mutateReviewStatus(w, r, storage.ReviewOutdated, storage.FeedbackOutdated)
}

// DeleteItem deactivates/rejects a memory.
// DELETE /memory/items/{id}
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	factID := chi.URLParam(r, "id")
	if _, err := h.KB.SetMemoryReviewStatus(r.Context(), userID, factID, storage.ReviewRejected); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "fact_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete_failed", "message": err.Error()})
		return
	}
	_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, &factID, nil, storage.FeedbackReject, map[string]any{"via": "delete"})
	_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ResolveItem resolves a conflict against an existing fact.
// POST /memory/items/{id}/resolve  {decision: keep_existing|accept_new, fact?: string}
func (h *Handler) ResolveItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	factID := chi.URLParam(r, "id")
	var body struct {
		Decision string  `json:"decision"`
		Fact     *string `json:"fact"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	decision := strings.TrimSpace(strings.ToLower(body.Decision))
	if decision != "keep_existing" && decision != "accept_new" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_decision"})
		return
	}

	existing, err := h.KB.GetMemoryFactByID(r.Context(), userID, factID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "fact_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed", "message": err.Error()})
		return
	}

	switch decision {
	case "keep_existing":
		if existing.ReviewStatus == storage.ReviewPendingReview {
			if _, err := h.KB.SetMemoryReviewStatus(r.Context(), userID, factID, storage.ReviewRejected); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve_failed", "message": err.Error()})
				return
			}
		}
		_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, &factID, nil, storage.FeedbackResolve, map[string]any{
			"decision": decision,
		})
		writeJSON(w, http.StatusOK, map[string]any{"status": "resolved", "decision": decision, "kept_fact_id": factID})
	case "accept_new":
		factText := existing.Fact
		if body.Fact != nil && strings.TrimSpace(*body.Fact) != "" {
			factText = strings.TrimSpace(*body.Fact)
		}
		if existing.ReviewStatus == storage.ReviewPendingReview {
			fact, err := h.KB.SetMemoryReviewStatus(r.Context(), userID, factID, storage.ReviewActive)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve_failed", "message": err.Error()})
				return
			}
			if factText != existing.Fact {
				patched, err := h.KB.UpdateMemoryFactFields(r.Context(), userID, factID, map[string]any{"fact": factText})
				if err == nil {
					fact = patched
				}
			}
			_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, &factID, nil, storage.FeedbackResolve, map[string]any{
				"decision": decision,
			})
			_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
			writeJSON(w, http.StatusOK, storage.MemoryItem{MemoryFact: fact})
			return
		}
		// Active conflict target: supersede with accept_new text.
		in := storage.MemoryFactInput{
			Fact:         factText,
			EntityName:   existing.EntityName,
			Topic:        existing.Topic,
			SourceID:     existing.SourceID,
			SupersedesID: &existing.ID,
			MemoryKind:   firstNonEmptyStr(ptrStr(existing.MemoryKind), storage.MemoryKindFact),
			Predicate:    ptrStr(existing.Predicate),
			ObjectValue:  existing.ObjectValue,
			Confidence:   1,
			Sensitivity:  existing.Sensitivity,
			ReviewStatus: storage.ReviewActive,
		}
		if err := h.KB.MarkMemoryFactSuperseded(r.Context(), userID, existing.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve_failed", "message": err.Error()})
			return
		}
		fact, err := h.KB.InsertMemoryFact(r.Context(), userID, in)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve_failed", "message": err.Error()})
			return
		}
		_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, &fact.ID, nil, storage.FeedbackResolve, map[string]any{
			"decision":         decision,
			"previous_fact_id": existing.ID,
		})
		_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
		writeJSON(w, http.StatusOK, storage.MemoryItem{MemoryFact: fact})
	}
}

// ListEvidence returns provenance for a fact.
// GET /memory/items/{id}/evidence
func (h *Handler) ListEvidence(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	factID := chi.URLParam(r, "id")
	if _, err := h.KB.GetMemoryFactByID(r.Context(), userID, factID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "fact_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed", "message": err.Error()})
		return
	}
	evidence, err := h.KB.ListMemoryEvidence(r.Context(), userID, factID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "evidence_failed", "message": err.Error()})
		return
	}
	if evidence == nil {
		evidence = []storage.MemoryEvidence{}
	}
	writeJSON(w, http.StatusOK, evidence)
}

// ListDerivedFromNote returns memories derived from a note.
// GET /memory/notes/{noteId}/derived
func (h *Handler) ListDerivedFromNote(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	noteID := chi.URLParam(r, "noteId")
	facts, err := h.KB.ListDerivedMemoriesForNote(r.Context(), userID, noteID, queryLimit(r, 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, factsToItems(facts))
}

// ListSuggestions lists pending memory suggestions for the review inbox.
// GET /memory/suggestions
func (h *Handler) ListSuggestions(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = storage.SuggestionPending
	}
	items, err := h.KB.ListMemorySuggestionsFiltered(r.Context(), userID, status, queryLimit(r, 100))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	if items == nil {
		items = []storage.MemorySuggestion{}
	}
	writeJSON(w, http.StatusOK, items)
}

// AcceptSuggestion activates a pending memory suggestion (creates fact + evidence).
// POST /memory/suggestions/{id}/accept
func (h *Handler) AcceptSuggestion(w http.ResponseWriter, r *http.Request) {
	h.resolveSuggestion(w, r, "accept")
}

// RejectSuggestion rejects a pending memory suggestion.
// POST /memory/suggestions/{id}/reject
func (h *Handler) RejectSuggestion(w http.ResponseWriter, r *http.Request) {
	h.resolveSuggestion(w, r, "reject")
}

// ResolveSuggestion resolves a conflicting suggestion.
// POST /memory/suggestions/{id}/resolve {decision: accept_new|keep_existing, fact?: string}
func (h *Handler) ResolveSuggestion(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	suggestionID := chi.URLParam(r, "id")
	var body struct {
		Decision string  `json:"decision"`
		Fact     *string `json:"fact"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	decision := strings.TrimSpace(strings.ToLower(body.Decision))
	if decision != "accept_new" && decision != "keep_existing" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_decision"})
		return
	}

	sug, err := h.KB.GetMemorySuggestionByID(r.Context(), userID, suggestionID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "suggestion_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed", "message": err.Error()})
		return
	}
	if sug.Status != storage.SuggestionPending {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already_resolved"})
		return
	}

	if decision == "keep_existing" {
		if err := h.KB.UpdateMemorySuggestionStatus(r.Context(), userID, suggestionID, storage.SuggestionRejected); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve_failed", "message": err.Error()})
			return
		}
		_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, sug.TargetFactID, &suggestionID, storage.FeedbackResolve, map[string]any{
			"decision": decision,
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": "resolved", "decision": decision})
		return
	}

	fact, err := h.activateSuggestion(r, userID, sug, body.Fact)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve_failed", "message": err.Error()})
		return
	}
	if err := h.KB.UpdateMemorySuggestionStatus(r.Context(), userID, suggestionID, storage.SuggestionAccepted); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve_failed", "message": err.Error()})
		return
	}
	_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, &fact.ID, &suggestionID, storage.FeedbackResolve, map[string]any{
		"decision": decision,
	})
	_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
	writeJSON(w, http.StatusOK, storage.MemoryItem{MemoryFact: fact, SuggestionID: &suggestionID})
}

// PostFeedback records citation/review feedback (Not relevant, Outdated, etc).
// POST /memory/feedback
func (h *Handler) PostFeedback(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	var body struct {
		FactID       *string        `json:"fact_id"`
		SuggestionID *string        `json:"suggestion_id"`
		Action       string         `json:"action"`
		Details      map[string]any `json:"details"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_body"})
		return
	}
	action := strings.TrimSpace(strings.ToLower(body.Action))
	switch action {
	case storage.FeedbackNotRelevant, storage.FeedbackOutdated, storage.FeedbackConfirm, storage.FeedbackReject, storage.FeedbackEdit:
	default:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_action"})
		return
	}
	if (body.FactID == nil || strings.TrimSpace(*body.FactID) == "") &&
		(body.SuggestionID == nil || strings.TrimSpace(*body.SuggestionID) == "") {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fact_or_suggestion_required"})
		return
	}

	fb, err := h.KB.InsertMemoryFeedback(r.Context(), userID, body.FactID, body.SuggestionID, action, body.Details)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "feedback_failed", "message": err.Error()})
		return
	}

	// Side effects for citation feedback on known facts.
	if body.FactID != nil && strings.TrimSpace(*body.FactID) != "" {
		factID := strings.TrimSpace(*body.FactID)
		switch action {
		case storage.FeedbackOutdated:
			_, _ = h.KB.SetMemoryReviewStatus(r.Context(), userID, factID, storage.ReviewOutdated)
			_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
		case storage.FeedbackNotRelevant:
			_, _ = h.KB.SetMemoryReviewStatus(r.Context(), userID, factID, storage.ReviewRejected)
			_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
		}
	}

	writeJSON(w, http.StatusOK, fb)
}

func (h *Handler) mutateReviewStatus(w http.ResponseWriter, r *http.Request, status, feedbackAction string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	factID := chi.URLParam(r, "id")
	existing, err := h.KB.GetMemoryFactByID(r.Context(), userID, factID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "fact_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed", "message": err.Error()})
		return
	}

	// Sensitive / restricted memories must pass through review before activation.
	if status == storage.ReviewActive &&
		(existing.Sensitivity == storage.SensitivitySensitive || existing.Sensitivity == storage.SensitivityRestricted) &&
		existing.ReviewStatus != storage.ReviewPendingReview &&
		existing.ReviewStatus != storage.ReviewActive {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "sensitive_requires_review"})
		return
	}

	fact, err := h.KB.SetMemoryReviewStatus(r.Context(), userID, factID, status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update_failed", "message": err.Error()})
		return
	}
	_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, &factID, nil, feedbackAction, map[string]any{
		"review_status": status,
	})
	_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
	writeJSON(w, http.StatusOK, storage.MemoryItem{MemoryFact: fact})
}

func (h *Handler) resolveSuggestion(w http.ResponseWriter, r *http.Request, action string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.kbReady(w) {
		return
	}
	suggestionID := chi.URLParam(r, "id")
	sug, err := h.KB.GetMemorySuggestionByID(r.Context(), userID, suggestionID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "suggestion_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed", "message": err.Error()})
		return
	}
	if sug.Status != storage.SuggestionPending {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already_resolved"})
		return
	}

	if action == "reject" {
		if err := h.KB.UpdateMemorySuggestionStatus(r.Context(), userID, suggestionID, storage.SuggestionRejected); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reject_failed", "message": err.Error()})
			return
		}
		_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, sug.TargetFactID, &suggestionID, storage.FeedbackReject, nil)
		writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
		return
	}

	// Accept: sensitive suggestions stay explicit (user clicked accept = review satisfied).
	sens := payloadString(sug.Payload, "sensitivity")
	if sens == storage.SensitivityRestricted {
		// Restricted still requires accept (this path), never silent auto-activate.
	}
	fact, err := h.activateSuggestion(r, userID, sug, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "accept_failed", "message": err.Error()})
		return
	}
	if err := h.KB.UpdateMemorySuggestionStatus(r.Context(), userID, suggestionID, storage.SuggestionAccepted); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "accept_failed", "message": err.Error()})
		return
	}
	_, _ = h.KB.InsertMemoryFeedback(r.Context(), userID, &fact.ID, &suggestionID, storage.FeedbackAccept, nil)
	_ = h.KB.UpsertProjectedProfileSummary(r.Context(), userID)
	writeJSON(w, http.StatusOK, storage.MemoryItem{MemoryFact: fact, SuggestionID: &suggestionID})
}

func (h *Handler) activateSuggestion(r *http.Request, userID string, sug storage.MemorySuggestion, factOverride *string) (storage.MemoryFact, error) {
	payload := sug.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	factText := payloadString(payload, "fact")
	if factOverride != nil && strings.TrimSpace(*factOverride) != "" {
		factText = strings.TrimSpace(*factOverride)
	}
	if factText == "" {
		factText = payloadString(payload, "predicate")
	}
	kind := payloadString(payload, "kind")
	if kind == "" {
		kind = storage.MemoryKindFact
	}
	sens := payloadString(payload, "sensitivity")
	if sens == "" {
		sens = storage.SensitivityNormal
	}
	pred := payloadString(payload, "predicate")
	entity := payloadString(payload, "entity_name")
	conf := 0.0
	if sug.Confidence != nil {
		conf = *sug.Confidence
	}
	if v, ok := payload["confidence"].(float64); ok {
		conf = v
	}

	in := storage.MemoryFactInput{
		Fact:         factText,
		MemoryKind:   kind,
		Predicate:    pred,
		Confidence:   conf,
		Sensitivity:  sens,
		ReviewStatus: storage.ReviewActive,
	}
	if entity != "" {
		in.EntityName = &entity
	}
	topic := kind
	in.Topic = &topic
	if v, ok := payload["value"].(map[string]any); ok {
		in.ObjectValue = v
	}
	if sid := payloadString(payload, "source_id"); sid != "" {
		in.SourceID = &sid
	}

	// Conflict accept_new: supersede the conflicting existing fact when present.
	if conflictID := payloadString(payload, "conflict_with_fact_id"); conflictID != "" {
		_ = h.KB.MarkMemoryFactSuperseded(r.Context(), userID, conflictID)
		in.SupersedesID = &conflictID
	}

	fact, err := h.KB.InsertMemoryFact(r.Context(), userID, in)
	if err != nil {
		return storage.MemoryFact{}, err
	}

	excerpt := payloadString(payload, "excerpt")
	sourceKind := payloadString(payload, "source_kind")
	if sourceKind == "" {
		sourceKind = storage.EvidenceManual
	}
	var sourceID *string
	if sid := payloadString(payload, "source_id"); sid != "" {
		sourceID = &sid
	}
	if excerpt != "" || sourceID != nil {
		_, _ = h.KB.InsertMemoryEvidence(r.Context(), userID, fact.ID, sourceKind, sourceID, excerpt, map[string]any{
			"from_suggestion": sug.ID,
		})
	}
	return fact, nil
}

func (h *Handler) listConflictingSuggestions(r *http.Request, userID string) ([]storage.MemoryItem, error) {
	suggestions, err := h.KB.ListMemorySuggestionsFiltered(r.Context(), userID, storage.SuggestionPending, 100)
	if err != nil {
		return nil, err
	}
	out := make([]storage.MemoryItem, 0)
	for _, s := range suggestions {
		item, include := suggestionToItem(s, false)
		if !include {
			continue
		}
		if item.Conflicting {
			out = append(out, item)
		}
	}
	return out, nil
}

func factsToItems(facts []storage.MemoryFact) []storage.MemoryItem {
	out := make([]storage.MemoryItem, 0, len(facts))
	for _, f := range facts {
		out = append(out, storage.MemoryItem{MemoryFact: f})
	}
	return out
}

func suggestionToItem(s storage.MemorySuggestion, sensitiveOnly bool) (storage.MemoryItem, bool) {
	payload := s.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	sens := payloadString(payload, "sensitivity")
	if sensitiveOnly && sens != storage.SensitivitySensitive && sens != storage.SensitivityRestricted {
		return storage.MemoryItem{}, false
	}
	conflicting, _ := payload["conflicting"].(bool)
	factText := payloadString(payload, "fact")
	kind := payloadString(payload, "kind")
	pred := payloadString(payload, "predicate")
	entity := payloadString(payload, "entity_name")
	var entityPtr *string
	if entity != "" {
		entityPtr = &entity
	}
	var kindPtr *string
	if kind != "" {
		kindPtr = &kind
	}
	var predPtr *string
	if pred != "" {
		predPtr = &pred
	}
	item := storage.MemoryItem{
		MemoryFact: storage.MemoryFact{
			ID:           "suggestion:" + s.ID,
			UserID:       s.UserID,
			Fact:         factText,
			EntityName:   entityPtr,
			MemoryKind:   kindPtr,
			Predicate:    predPtr,
			Confidence:   s.Confidence,
			Sensitivity:  firstNonEmptyStr(sens, storage.SensitivityNormal),
			ReviewStatus: storage.ReviewPendingReview,
			Active:       false,
			CreatedAt:    s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		},
		Conflicting:  conflicting,
		SuggestionID: &s.ID,
	}
	if excerpt := payloadString(payload, "excerpt"); excerpt != "" {
		item.Evidence = []storage.MemoryEvidence{{
			SourceKind: firstNonEmptyStr(payloadString(payload, "source_kind"), storage.EvidenceManual),
			Excerpt:    excerpt,
		}}
	}
	return item, true
}

func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return "", false
	}
	return userID, true
}

func (h *Handler) kbReady(w http.ResponseWriter) bool {
	if h.KB == nil || !h.KB.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory_disabled"})
		return false
	}
	return true
}

func memoryKindLabel(kind string) string {
	switch kind {
	case storage.MemoryKindIdentity:
		return "Identity"
	case storage.MemoryKindPreference:
		return "Preferences"
	case storage.MemoryKindRelationship:
		return "Relationships"
	case storage.MemoryKindProject:
		return "Projects"
	case storage.MemoryKindGoal:
		return "Goals"
	case storage.MemoryKindRoutine:
		return "Routines"
	case storage.MemoryKindEvent:
		return "Events"
	case storage.MemoryKindConstraint:
		return "Constraints"
	case storage.MemoryKindInstruction:
		return "Instructions"
	default:
		return "Other"
	}
}

func payloadString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return ""
	}
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func coalescePtr(preferred, fallback *string) *string {
	if preferred != nil {
		s := strings.TrimSpace(*preferred)
		if s == "" {
			return preferred
		}
		return &s
	}
	return fallback
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
