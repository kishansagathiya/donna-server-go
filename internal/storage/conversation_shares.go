package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ConversationShare is an owned share link row (auth'd APIs).
type ConversationShare struct {
	ID             string  `json:"id"`
	ConversationID string  `json:"conversation_id"`
	Token          string  `json:"token"`
	CreatedAt      string  `json:"created_at"`
	RevokedAt      *string `json:"revoked_at,omitempty"`
	ExpiresAt      *string `json:"expires_at,omitempty"`
}

// PublicSharedTurn is a safe turn payload for unauthenticated viewers.
type PublicSharedTurn struct {
	TurnIndex           int                    `json:"turn_index"`
	UserTranscript      string                 `json:"user_transcript"`
	AssistantTranscript string                 `json:"assistant_transcript"`
	CreatedAt           string                 `json:"created_at"`
	Attachments         []StoredTurnAttachment `json:"attachments,omitempty"`
}

// PublicSharedConversation is returned by GET /share/{token}.
type PublicSharedConversation struct {
	Title     string             `json:"title"`
	Channel   string             `json:"channel"`
	CreatedAt string             `json:"created_at"`
	Turns     []PublicSharedTurn `json:"turns"`
}

type shareRow struct {
	ID             string  `json:"id"`
	ConversationID string  `json:"conversation_id"`
	UserID         string  `json:"user_id"`
	Token          string  `json:"token"`
	CreatedAt      string  `json:"created_at"`
	RevokedAt      *string `json:"revoked_at"`
	ExpiresAt      *string `json:"expires_at"`
}

func newShareToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// URL-safe, no padding — suitable for path segments.
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (c *Conversations) getActiveShare(ctx context.Context, userID, conversationID string) (*ConversationShare, error) {
	q := url.Values{}
	q.Set("select", "id,conversation_id,token,created_at,revoked_at,expires_at")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("user_id", "eq."+userID)
	q.Set("revoked_at", "is.null")
	q.Set("limit", "1")

	var rows []shareRow
	if err := c.DB.Get(ctx, "conversation_shares", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	row := rows[0]
	if shareExpired(row.ExpiresAt) {
		return nil, nil
	}
	return &ConversationShare{
		ID:             row.ID,
		ConversationID: row.ConversationID,
		Token:          row.Token,
		CreatedAt:      row.CreatedAt,
		RevokedAt:      row.RevokedAt,
		ExpiresAt:      row.ExpiresAt,
	}, nil
}

// GetShareForUser returns the active share for an owned conversation, if any.
func (c *Conversations) GetShareForUser(ctx context.Context, userID, conversationID string) (*ConversationShare, error) {
	if !c.Enabled {
		return nil, fmt.Errorf("conversation persistence disabled")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("missing conversation id")
	}
	owned, err := c.ownsConversation(ctx, userID, conversationID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, fmt.Errorf("conversation not found")
	}
	return c.getActiveShare(ctx, userID, conversationID)
}

// CreateShare ensures an active share token exists for the conversation.
// If one already exists, it is returned (idempotent).
func (c *Conversations) CreateShare(ctx context.Context, userID, conversationID string) (*ConversationShare, error) {
	if !c.Enabled {
		return nil, fmt.Errorf("conversation persistence disabled")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("missing conversation id")
	}

	owned, err := c.ownsConversation(ctx, userID, conversationID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, fmt.Errorf("conversation not found")
	}

	existing, err := c.getActiveShare(ctx, userID, conversationID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	token, err := newShareToken()
	if err != nil {
		return nil, err
	}

	var rows []shareRow
	body := map[string]any{
		"conversation_id": conversationID,
		"user_id":         userID,
		"token":           token,
	}
	if err := c.DB.Insert(ctx, "conversation_shares", body, &rows); err != nil {
		// Race: another request may have created the unique active share.
		if existing, getErr := c.getActiveShare(ctx, userID, conversationID); getErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("failed to create share")
	}
	row := rows[0]
	return &ConversationShare{
		ID:             row.ID,
		ConversationID: row.ConversationID,
		Token:          row.Token,
		CreatedAt:      row.CreatedAt,
		RevokedAt:      row.RevokedAt,
		ExpiresAt:      row.ExpiresAt,
	}, nil
}

// RevokeShare soft-revokes the active share for an owned conversation.
func (c *Conversations) RevokeShare(ctx context.Context, userID, conversationID string) error {
	if !c.Enabled {
		return fmt.Errorf("conversation persistence disabled")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return fmt.Errorf("missing conversation id")
	}

	owned, err := c.ownsConversation(ctx, userID, conversationID)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("conversation not found")
	}

	q := url.Values{}
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("user_id", "eq."+userID)
	q.Set("revoked_at", "is.null")

	body := map[string]any{
		"revoked_at": time.Now().UTC().Format(time.RFC3339),
	}
	if err := c.DB.Patch(ctx, "conversation_shares", q, body); err != nil {
		return err
	}
	return nil
}

// GetPublicByShareToken resolves a public share token to a safe conversation view.
func (c *Conversations) GetPublicByShareToken(ctx context.Context, token string) (*PublicSharedConversation, error) {
	if !c.Enabled {
		return nil, fmt.Errorf("conversation persistence disabled")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("missing token")
	}

	q := url.Values{}
	q.Set("select", "id,conversation_id,user_id,token,created_at,revoked_at,expires_at")
	q.Set("token", "eq."+token)
	q.Set("revoked_at", "is.null")
	q.Set("limit", "1")

	var rows []shareRow
	if err := c.DB.Get(ctx, "conversation_shares", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 || shareExpired(rows[0].ExpiresAt) {
		return nil, nil
	}
	share := rows[0]

	convQ := url.Values{}
	convQ.Set("select", "id,channel,title,title_source,created_at")
	convQ.Set("id", "eq."+share.ConversationID)
	convQ.Set("user_id", "eq."+share.UserID)
	convQ.Set("limit", "1")

	var convRows []struct {
		ID          string `json:"id"`
		Channel     string `json:"channel"`
		Title       string `json:"title"`
		TitleSource string `json:"title_source"`
		CreatedAt   string `json:"created_at"`
	}
	if err := c.DB.Get(ctx, "conversations", convQ, &convRows); err != nil {
		return nil, err
	}
	if len(convRows) == 0 {
		return nil, nil
	}

	turns, err := c.loadTurns(ctx, share.ConversationID)
	if err != nil {
		return nil, err
	}

	row := convRows[0]
	title := strings.TrimSpace(row.Title)
	if title == "" && len(turns) > 0 {
		title = deriveConversationTitle(turns[0].UserTranscript, turns[0].AssistantTranscript)
	}
	if title == "" {
		title = fallbackConversationTitle(row.Channel, row.CreatedAt)
	}

	publicTurns := make([]PublicSharedTurn, 0, len(turns))
	for _, turn := range turns {
		publicTurns = append(publicTurns, PublicSharedTurn{
			TurnIndex:           turn.TurnIndex,
			UserTranscript:      turn.UserTranscript,
			AssistantTranscript: turn.AssistantTranscript,
			CreatedAt:           turn.CreatedAt,
			Attachments:         turn.Attachments,
		})
	}

	return &PublicSharedConversation{
		Title:     title,
		Channel:   row.Channel,
		CreatedAt: row.CreatedAt,
		Turns:     publicTurns,
	}, nil
}

func shareExpired(expiresAt *string) bool {
	if expiresAt == nil {
		return false
	}
	raw := strings.TrimSpace(*expiresAt)
	if raw == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		// Also accept timestamptz with fractional seconds from PostgREST.
		t, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return false
		}
	}
	return time.Now().UTC().After(t.UTC())
}
