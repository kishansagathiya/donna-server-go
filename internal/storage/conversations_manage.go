package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

// TitleGenerator produces a short conversation title from the first turn.
// Optional — when nil, titles stay truncated from the first message.
type TitleGenerator interface {
	GenerateConversationTitle(ctx context.Context, userText, assistantText string) (string, error)
}

// ListOptions controls conversation listing / search.
type ListOptions struct {
	Limit           int
	IncludeArchived bool
	ArchivedOnly    bool
	Query           string // keyword search over title + turn text
	Tag             string // filter by conversation tag
}

// UpdateConversationInput is a partial update. Nil pointers mean "leave unchanged".
type UpdateConversationInput struct {
	Title    *string
	Archived *bool
	Pinned   *bool
	Tags     *[]string
}

// UpdateConversation applies rename / archive / pin / tags for an owned conversation.
// Soft-archive uses archived_at; hard delete is DeleteForUser.
func (c *Conversations) UpdateConversation(ctx context.Context, userID, conversationID string, input UpdateConversationInput) (*ConversationSummary, error) {
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

	patch := map[string]any{}
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" {
			return nil, fmt.Errorf("title must not be empty")
		}
		if len(title) > 120 {
			title = title[:117] + "..."
		}
		patch["title"] = title
		patch["title_source"] = "user"
	}
	if input.Archived != nil {
		if *input.Archived {
			patch["archived_at"] = time.Now().UTC().Format(time.RFC3339)
		} else {
			patch["archived_at"] = nil
		}
	}
	if input.Pinned != nil {
		if *input.Pinned {
			patch["pinned_at"] = time.Now().UTC().Format(time.RFC3339)
		} else {
			patch["pinned_at"] = nil
		}
	}

	if len(patch) > 0 {
		q := url.Values{}
		q.Set("id", "eq."+conversationID)
		q.Set("user_id", "eq."+userID)
		if err := c.DB.Patch(ctx, "conversations", q, patch); err != nil {
			return nil, err
		}
	}

	if input.Tags != nil {
		if _, err := c.SetTags(ctx, userID, conversationID, *input.Tags); err != nil {
			return nil, err
		}
	}

	detail, err := c.GetWithTurns(ctx, userID, conversationID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, fmt.Errorf("conversation not found")
	}

	summary := ConversationSummary{
		ID:              detail.ID,
		Channel:         detail.Channel,
		Title:           detail.Title,
		TitleSource:     detail.TitleSource,
		ClientSessionID: detail.ClientSessionID,
		VoiceSessionID:  detail.VoiceSessionID,
		TurnCount:       len(detail.Turns),
		CreatedAt:       detail.CreatedAt,
		UpdatedAt:       detail.CreatedAt,
		EndedAt:         detail.EndedAt,
		ArchivedAt:      detail.ArchivedAt,
		PinnedAt:        detail.PinnedAt,
		Tags:            detail.Tags,
	}
	if len(detail.Turns) > 0 {
		summary.Preview = truncatePreview(latestTurnSnippet(detail.Turns))
		summary.UpdatedAt = detail.Turns[len(detail.Turns)-1].CreatedAt
	}
	return &summary, nil
}

// DeleteForUser hard-deletes a conversation (turns and tags cascade).
func (c *Conversations) DeleteForUser(ctx context.Context, userID, conversationID string) error {
	if !c.Enabled {
		return fmt.Errorf("conversation persistence disabled")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return fmt.Errorf("missing conversation id")
	}

	q := url.Values{}
	q.Set("id", "eq."+conversationID)
	q.Set("user_id", "eq."+userID)
	if err := c.DB.Delete(ctx, "conversations", q); err != nil {
		return err
	}

	log.Print("conversation deleted", map[string]any{
		"conversationId": conversationID,
		"userId":         log.ShortID(userID),
	})
	return nil
}

// SetTags replaces the tag set on a conversation.
func (c *Conversations) SetTags(ctx context.Context, userID, conversationID string, tags []string) ([]string, error) {
	if !c.Enabled {
		return nil, fmt.Errorf("conversation persistence disabled")
	}
	var rows []struct {
		Tag string `json:"tag"`
	}
	if err := c.DB.RPC(ctx, "set_conversation_tags", map[string]any{
		"p_user_id":         userID,
		"p_conversation_id": conversationID,
		"p_tags":            normalizeConversationTags(tags),
	}, &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if tag := strings.TrimSpace(row.Tag); tag != "" {
			out = append(out, tag)
		}
	}
	return out, nil
}

// ListTags returns distinct tags used on the user's conversations.
func (c *Conversations) ListTags(ctx context.Context, userID string, limit int) ([]string, error) {
	if !c.Enabled {
		return nil, fmt.Errorf("conversation persistence disabled")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	q := url.Values{}
	q.Set("select", "tag")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "tag.asc")
	q.Set("limit", fmt.Sprintf("%d", limit*4)) // over-fetch then dedupe

	var rows []struct {
		Tag string `json:"tag"`
	}
	if err := c.DB.Get(ctx, "conversation_tags", q, &rows); err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, row := range rows {
		tag := strings.TrimSpace(row.Tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (c *Conversations) getTagsForConversations(ctx context.Context, userID string, conversationIDs []string) (map[string][]string, error) {
	result := make(map[string][]string, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return result, nil
	}

	q := url.Values{}
	q.Set("select", "conversation_id,tag")
	q.Set("user_id", "eq."+userID)
	q.Set("conversation_id", "in.("+strings.Join(conversationIDs, ",")+")")
	q.Set("order", "tag.asc")

	var rows []struct {
		ConversationID string `json:"conversation_id"`
		Tag            string `json:"tag"`
	}
	if err := c.DB.Get(ctx, "conversation_tags", q, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ConversationID] = append(result[row.ConversationID], row.Tag)
	}
	return result, nil
}

func (c *Conversations) getTagsForConversation(ctx context.Context, userID, conversationID string) ([]string, error) {
	m, err := c.getTagsForConversations(ctx, userID, []string{conversationID})
	if err != nil {
		return nil, err
	}
	tags := m[conversationID]
	if tags == nil {
		return []string{}, nil
	}
	return tags, nil
}

func (c *Conversations) ownsConversation(ctx context.Context, userID, conversationID string) (bool, error) {
	q := url.Values{}
	q.Set("select", "id")
	q.Set("id", "eq."+conversationID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")

	var rows []struct {
		ID string `json:"id"`
	}
	if err := c.DB.Get(ctx, "conversations", q, &rows); err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (c *Conversations) searchConversationIDs(ctx context.Context, userID string, opts ListOptions) ([]string, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	var rows []struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := c.DB.RPC(ctx, "search_conversations", map[string]any{
		"p_user_id":          userID,
		"p_query":            strings.TrimSpace(opts.Query),
		"p_limit":            limit,
		"p_include_archived": opts.IncludeArchived || opts.ArchivedOnly,
	}, &rows); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if id := strings.TrimSpace(row.ConversationID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (c *Conversations) conversationIDsForTag(ctx context.Context, userID, tag string) ([]string, error) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return nil, nil
	}

	q := url.Values{}
	q.Set("select", "conversation_id")
	q.Set("user_id", "eq."+userID)
	q.Set("tag", "eq."+tag)

	var rows []struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := c.DB.Get(ctx, "conversation_tags", q, &rows); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ConversationID)
	}
	return ids, nil
}

func normalizeConversationTags(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if len(tag) > 40 {
			tag = tag[:40]
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// SetLLMTitle updates the title only when title_source is still "auto"
// (truncated placeholder). User renames (title_source=user) are never overwritten.
func (c *Conversations) SetLLMTitle(ctx context.Context, conversationID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	if len(title) > 80 {
		title = title[:77] + "..."
	}
	q := url.Values{}
	q.Set("id", "eq."+conversationID)
	q.Set("title_source", "eq.auto")
	return c.DB.Patch(ctx, "conversations", q, map[string]string{
		"title":        title,
		"title_source": "llm",
	})
}

func (c *Conversations) maybeGenerateTitleAsync(conversationID, userText, assistantText string) {
	if c.TitleGen == nil || strings.TrimSpace(conversationID) == "" {
		return
	}
	go func() {
		ctx := context.Background()
		title, err := c.TitleGen.GenerateConversationTitle(ctx, userText, assistantText)
		if err != nil {
			log.Warn("llm conversation title failed", map[string]any{
				"conversationId": conversationID,
				"error":          err.Error(),
			})
			return
		}
		title = strings.TrimSpace(title)
		if title == "" {
			return
		}
		// Strip wrapping quotes models sometimes add.
		title = strings.Trim(title, `"'`)
		if err := c.SetLLMTitle(ctx, conversationID, title); err != nil {
			log.Warn("failed to persist llm conversation title", map[string]any{
				"conversationId": conversationID,
				"error":          err.Error(),
			})
		}
	}()
}
