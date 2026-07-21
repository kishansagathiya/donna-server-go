package storage

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// MemorySuggestion is a pending auto-suggestion (tag or memory).
type MemorySuggestion struct {
	ID             string         `json:"id"`
	UserID         string         `json:"user_id"`
	SuggestionKind string         `json:"suggestion_kind"`
	Status         string         `json:"status"`
	TargetNoteID   *string        `json:"target_note_id,omitempty"`
	TargetFactID   *string        `json:"target_fact_id,omitempty"`
	Payload        map[string]any `json:"payload"`
	Confidence     *float64       `json:"confidence,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
}

// MemoryFeedback records user corrections for enrichment / citation feedback.
type MemoryFeedback struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id"`
	FactID       *string        `json:"fact_id,omitempty"`
	SuggestionID *string        `json:"suggestion_id,omitempty"`
	Action       string         `json:"action"`
	Details      map[string]any `json:"details"`
	CreatedAt    time.Time      `json:"created_at"`
}

// InsertTagSuggestion stores a medium-confidence tag suggestion.
func (n *Notes) InsertTagSuggestion(ctx context.Context, userID, noteID, tag string, confidence float64) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	body := map[string]any{
		"user_id":         userID,
		"suggestion_kind": "tag",
		"status":          "pending",
		"target_note_id":  noteID,
		"confidence":      confidence,
		"payload": map[string]any{
			"tag": tag,
		},
	}
	var dest []MemorySuggestion
	return n.DB.Insert(ctx, "memory_suggestions", body, &dest)
}

// ListPendingTagSuggestions returns pending tag suggestions for a note or user.
func (n *Notes) ListPendingTagSuggestions(ctx context.Context, userID string, noteID string, limit int) ([]MemorySuggestion, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{}
	q.Set("select", "id,user_id,suggestion_kind,status,target_note_id,payload,confidence,created_at,resolved_at")
	q.Set("user_id", "eq."+userID)
	q.Set("suggestion_kind", "eq.tag")
	q.Set("status", "eq.pending")
	if noteID != "" {
		q.Set("target_note_id", "eq."+noteID)
	}
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []MemorySuggestion
	if err := n.DB.Get(ctx, "memory_suggestions", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// UpdateSuggestionStatus accepts or rejects a suggestion.
func (n *Notes) UpdateSuggestionStatus(ctx context.Context, userID, suggestionID, status string) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	q := url.Values{}
	q.Set("id", "eq."+suggestionID)
	q.Set("user_id", "eq."+userID)
	body := map[string]any{
		"status": status,
	}
	if status == "accepted" || status == "rejected" {
		body["resolved_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return n.DB.Patch(ctx, "memory_suggestions", q, body)
}

// InsertTagCorrection records a tag_correction feedback row.
func (n *Notes) InsertTagCorrection(ctx context.Context, userID string, details map[string]any) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	body := map[string]any{
		"user_id": userID,
		"action":  "tag_correction",
		"details": details,
	}
	var dest []MemoryFeedback
	return n.DB.Insert(ctx, "memory_feedback", body, &dest)
}

// ListRecentTagCorrections returns recent tag_correction feedback for enricher context.
func (n *Notes) ListRecentTagCorrections(ctx context.Context, userID string, limit int) ([]MemoryFeedback, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{}
	q.Set("select", "id,user_id,action,details,created_at")
	q.Set("user_id", "eq."+userID)
	q.Set("action", "eq.tag_correction")
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []MemoryFeedback
	if err := n.DB.Get(ctx, "memory_feedback", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
