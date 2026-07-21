package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// MemoryListFilter selects memories for review/list APIs.
type MemoryListFilter struct {
	Status string // active|pending|sensitive|conflicting|rejected|outdated|all
	Kind   string
	Query  string
	Limit  int
	Offset int
}

// MemoryItem is a V2 fact returned by review/list APIs, optionally with evidence.
type MemoryItem struct {
	MemoryFact
	Evidence     []MemoryEvidence `json:"evidence,omitempty"`
	Conflicting  bool             `json:"conflicting,omitempty"`
	SuggestionID *string          `json:"suggestion_id,omitempty"`
}

// MemoryGroup is one UI group in the Memory screen.
type MemoryGroup struct {
	Kind  string       `json:"kind"`
	Label string       `json:"label"`
	Items []MemoryItem `json:"items"`
}

// GetMemoryFactByID loads a V2 fact regardless of active flag (owner-scoped).
func (k *Knowledge) GetMemoryFactByID(ctx context.Context, userID, factID string) (MemoryFact, error) {
	if !k.Enabled {
		return MemoryFact{}, fmt.Errorf("knowledge disabled")
	}
	q := url.Values{}
	q.Set("select", memoryFactSelect)
	q.Set("id", "eq."+factID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []MemoryFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return MemoryFact{}, err
	}
	if len(rows) == 0 {
		return MemoryFact{}, fmt.Errorf("fact not found")
	}
	return rows[0], nil
}

// ListMemoryFactsFiltered returns facts for review inbox / Memory UI filters.
func (k *Knowledge) ListMemoryFactsFiltered(ctx context.Context, userID string, f MemoryListFilter) ([]MemoryFact, error) {
	if !k.Enabled {
		return nil, nil
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}

	q := url.Values{}
	q.Set("select", memoryFactSelect)
	q.Set("user_id", "eq."+userID)
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	if f.Offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", f.Offset))
	}

	status := strings.TrimSpace(strings.ToLower(f.Status))
	switch status {
	case "", "active":
		q.Set("active", "eq.true")
		q.Set("or", "(review_status.eq.active,review_status.is.null)")
	case "pending":
		q.Set("review_status", "eq."+ReviewPendingReview)
	case "sensitive":
		q.Set("sensitivity", "in.(sensitive,restricted)")
		q.Set("or", "(review_status.eq.pending_review,review_status.eq.active)")
	case "rejected":
		q.Set("review_status", "eq."+ReviewRejected)
	case "outdated":
		q.Set("review_status", "eq."+ReviewOutdated)
	case "conflicting":
		// Conflicting candidates live primarily in memory_suggestions; return empty here.
		return []MemoryFact{}, nil
	case "all":
		// no extra filter
	default:
		q.Set("active", "eq.true")
		q.Set("or", "(review_status.eq.active,review_status.is.null)")
	}

	if kind := strings.TrimSpace(f.Kind); kind != "" {
		kinds := expandKindFilter(kind)
		if len(kinds) == 1 {
			q.Set("memory_kind", "eq."+kinds[0])
		} else if len(kinds) > 1 {
			q.Set("memory_kind", "in.("+strings.Join(kinds, ",")+")")
		}
	}

	if trimmed := strings.TrimSpace(f.Query); trimmed != "" {
		q.Set("search_vector", "fts.english.websearch."+trimmed)
	}

	var rows []MemoryFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func expandKindFilter(kind string) []string {
	switch NormalizeMemoryKindForUI(kind) {
	case MemoryKindRoutine:
		return []string{MemoryKindRoutine, MemoryKindHabit}
	case MemoryKindOther:
		return []string{MemoryKindOther, MemoryKindFact, MemoryKindLocation}
	default:
		k := NormalizeMemoryKindForUI(kind)
		if k == "" {
			return nil
		}
		return []string{k}
	}
}

// UpdateMemoryFactFields patches editable V2 columns and returns the row.
func (k *Knowledge) UpdateMemoryFactFields(ctx context.Context, userID, factID string, patch map[string]any) (MemoryFact, error) {
	if !k.Enabled {
		return MemoryFact{}, fmt.Errorf("knowledge disabled")
	}
	if len(patch) == 0 {
		return k.GetMemoryFactByID(ctx, userID, factID)
	}
	q := url.Values{}
	q.Set("id", "eq."+factID)
	q.Set("user_id", "eq."+userID)
	if err := k.DB.Patch(ctx, "kb_facts", q, patch); err != nil {
		return MemoryFact{}, err
	}
	return k.GetMemoryFactByID(ctx, userID, factID)
}

// SetMemoryReviewStatus updates review_status and active flag consistently.
func (k *Knowledge) SetMemoryReviewStatus(ctx context.Context, userID, factID, status string) (MemoryFact, error) {
	status = strings.TrimSpace(strings.ToLower(status))
	active := status == ReviewActive
	patch := map[string]any{
		"review_status": status,
		"active":        active,
	}
	if status == ReviewOutdated {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		patch["valid_until"] = now
		patch["active"] = false
	}
	return k.UpdateMemoryFactFields(ctx, userID, factID, patch)
}

// ListMemoryEvidence returns provenance rows for a fact.
func (k *Knowledge) ListMemoryEvidence(ctx context.Context, userID, factID string) ([]MemoryEvidence, error) {
	if !k.Enabled {
		return nil, nil
	}
	q := url.Values{}
	q.Set("select", "id,user_id,fact_id,source_kind,source_id,excerpt,metadata,created_at")
	q.Set("user_id", "eq."+userID)
	q.Set("fact_id", "eq."+factID)
	q.Set("order", "created_at.desc")
	q.Set("limit", "50")
	var rows []MemoryEvidence
	if err := k.DB.Get(ctx, "kb_memory_evidence", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ListDerivedMemoriesForNote returns facts linked to a note via evidence or source_id.
func (k *Knowledge) ListDerivedMemoriesForNote(ctx context.Context, userID, noteID string, limit int) ([]MemoryFact, error) {
	if !k.Enabled || strings.TrimSpace(noteID) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	seen := map[string]struct{}{}
	out := make([]MemoryFact, 0)

	eq := url.Values{}
	eq.Set("select", "fact_id")
	eq.Set("user_id", "eq."+userID)
	eq.Set("source_kind", "eq."+EvidenceNote)
	eq.Set("source_id", "eq."+noteID)
	eq.Set("limit", fmt.Sprintf("%d", limit))
	var evidenceRows []struct {
		FactID string `json:"fact_id"`
	}
	if err := k.DB.Get(ctx, "kb_memory_evidence", eq, &evidenceRows); err != nil {
		return nil, err
	}
	for _, row := range evidenceRows {
		if row.FactID == "" {
			continue
		}
		if _, ok := seen[row.FactID]; ok {
			continue
		}
		fact, err := k.GetMemoryFactByID(ctx, userID, row.FactID)
		if err != nil {
			continue
		}
		seen[row.FactID] = struct{}{}
		out = append(out, fact)
	}

	fq := url.Values{}
	fq.Set("select", memoryFactSelect)
	fq.Set("user_id", "eq."+userID)
	fq.Set("source_id", "eq."+noteID)
	fq.Set("order", "created_at.desc")
	fq.Set("limit", fmt.Sprintf("%d", limit))
	var sourceFacts []MemoryFact
	if err := k.DB.Get(ctx, "kb_facts", fq, &sourceFacts); err != nil {
		return nil, err
	}
	for _, fact := range sourceFacts {
		if _, ok := seen[fact.ID]; ok {
			continue
		}
		seen[fact.ID] = struct{}{}
		out = append(out, fact)
	}
	return out, nil
}

// ListMemorySuggestionsFiltered lists memory suggestions (not tag suggestions).
func (k *Knowledge) ListMemorySuggestionsFiltered(ctx context.Context, userID, status string, limit int) ([]MemorySuggestion, error) {
	if !k.Enabled {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	q := url.Values{}
	q.Set("select", "id,user_id,suggestion_kind,status,target_note_id,target_fact_id,payload,confidence,created_at,resolved_at")
	q.Set("user_id", "eq."+userID)
	q.Set("suggestion_kind", "eq."+SuggestionKindMemory)
	if s := strings.TrimSpace(status); s != "" {
		q.Set("status", "eq."+s)
	}
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	var rows []MemorySuggestion
	if err := k.DB.Get(ctx, "memory_suggestions", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// GetMemorySuggestionByID loads one suggestion for the user.
func (k *Knowledge) GetMemorySuggestionByID(ctx context.Context, userID, suggestionID string) (MemorySuggestion, error) {
	if !k.Enabled {
		return MemorySuggestion{}, fmt.Errorf("knowledge disabled")
	}
	q := url.Values{}
	q.Set("select", "id,user_id,suggestion_kind,status,target_note_id,target_fact_id,payload,confidence,created_at,resolved_at")
	q.Set("id", "eq."+suggestionID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []MemorySuggestion
	if err := k.DB.Get(ctx, "memory_suggestions", q, &rows); err != nil {
		return MemorySuggestion{}, err
	}
	if len(rows) == 0 {
		return MemorySuggestion{}, fmt.Errorf("suggestion not found")
	}
	return rows[0], nil
}

// UpdateMemorySuggestionStatus accepts/rejects a memory suggestion.
func (k *Knowledge) UpdateMemorySuggestionStatus(ctx context.Context, userID, suggestionID, status string) error {
	if !k.Enabled {
		return fmt.Errorf("knowledge disabled")
	}
	q := url.Values{}
	q.Set("id", "eq."+suggestionID)
	q.Set("user_id", "eq."+userID)
	body := map[string]any{"status": status}
	if status == SuggestionAccepted || status == SuggestionRejected {
		body["resolved_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return k.DB.Patch(ctx, "memory_suggestions", q, body)
}

// InsertMemoryFeedback writes a review/citation feedback row.
func (k *Knowledge) InsertMemoryFeedback(ctx context.Context, userID string, factID, suggestionID *string, action string, details map[string]any) (MemoryFeedback, error) {
	if !k.Enabled {
		return MemoryFeedback{}, fmt.Errorf("knowledge disabled")
	}
	if details == nil {
		details = map[string]any{}
	}
	body := map[string]any{
		"user_id": userID,
		"action":  action,
		"details": details,
	}
	if factID != nil && strings.TrimSpace(*factID) != "" {
		body["fact_id"] = strings.TrimSpace(*factID)
	}
	if suggestionID != nil && strings.TrimSpace(*suggestionID) != "" {
		body["suggestion_id"] = strings.TrimSpace(*suggestionID)
	}
	var dest []MemoryFeedback
	if err := k.DB.Insert(ctx, "memory_feedback", body, &dest); err != nil {
		return MemoryFeedback{}, err
	}
	if len(dest) == 0 {
		return MemoryFeedback{}, fmt.Errorf("failed to insert memory feedback")
	}
	return dest[0], nil
}
