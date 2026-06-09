package storage

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

const knowledgeBucket = "knowledge-assets"

type KbSource struct {
	ID             string         `json:"id"`
	UserID         string         `json:"user_id"`
	SourceType     string         `json:"source_type"`
	Content        string         `json:"content"`
	ConversationID *string        `json:"conversation_id"`
	TurnIndex      *int           `json:"turn_index"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type KbFact struct {
	ID         string  `json:"id"`
	UserID     string  `json:"user_id"`
	Fact       string  `json:"fact"`
	EntityName *string `json:"entity_name"`
	Topic      *string `json:"topic"`
	SourceID   *string `json:"source_id"`
	Active     bool    `json:"active"`
}

type ConversationTurn struct {
	TurnIndex            int    `json:"turn_index"`
	UserTranscript       string `json:"user_transcript"`
	AssistantTranscript  string `json:"assistant_transcript"`
}

type NewFactInput struct {
	Fact         string
	EntityName   *string
	Topic        *string
	SourceID     *string
	SupersedesID *string
}

func (k *Knowledge) LogKnowledge(message string, data map[string]any) {
	log.Print(message, data)
}

func (k *Knowledge) UpsertUserProfileSummary(ctx context.Context, userID, summary string) error {
	body := map[string]any{
		"user_id":    userID,
		"summary":    summary,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	var rows []struct {
		UserID string `json:"user_id"`
	}
	return k.DB.Upsert(ctx, "kb_user_profiles", "user_id", body, &rows)
}

func (k *Knowledge) GetActiveFacts(ctx context.Context, userID string) ([]KbFact, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,fact,entity_name,topic,source_id,active")
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")
	q.Set("order", "created_at.asc")

	var rows []KbFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (k *Knowledge) InsertFacts(ctx context.Context, userID string, facts []NewFactInput) (int, error) {
	if len(facts) == 0 {
		return 0, nil
	}
	rows := make([]map[string]any, 0, len(facts))
	for _, f := range facts {
		rows = append(rows, map[string]any{
			"user_id":       userID,
			"fact":          f.Fact,
			"entity_name":   f.EntityName,
			"topic":         f.Topic,
			"source_id":     f.SourceID,
			"supersedes_id": f.SupersedesID,
			"active":        true,
		})
	}
	if err := k.DB.Insert(ctx, "kb_facts", rows, nil); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func (k *Knowledge) DeactivateFact(ctx context.Context, factID string) error {
	q := url.Values{}
	q.Set("id", "eq."+factID)
	return k.DB.Patch(ctx, "kb_facts", q, map[string]any{"active": false})
}

func (k *Knowledge) GetConversationTurns(ctx context.Context, conversationID string) ([]ConversationTurn, error) {
	q := url.Values{}
	q.Set("select", "turn_index,user_transcript,assistant_transcript")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("order", "turn_index.asc")

	var rows []ConversationTurn
	if err := k.DB.Get(ctx, "conversation_turns", q, &rows); err != nil {
		return nil, fmt.Errorf("failed to load conversation turns: %w", err)
	}
	return rows, nil
}

func (k *Knowledge) WaitForConversationTurns(ctx context.Context, conversationID string) ([]ConversationTurn, error) {
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		turns, err := k.GetConversationTurns(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		if len(turns) > 0 {
			return turns, nil
		}
		select {
		case <-ctx.Done():
			return k.GetConversationTurns(ctx, conversationID)
		case <-time.After(time.Second):
		}
	}
	return k.GetConversationTurns(ctx, conversationID)
}

func (k *Knowledge) UpsertVoiceSource(ctx context.Context, input struct {
	UserID, ConversationID, UserTranscript, AssistantTranscript string
	TurnIndex                                                   int
}) (string, error) {
	content := "User: " + input.UserTranscript + "\nAssistant: " + input.AssistantTranscript
	body := map[string]any{
		"user_id":         input.UserID,
		"source_type":     "voice_turn",
		"content":         content,
		"conversation_id": input.ConversationID,
		"turn_index":      input.TurnIndex,
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := k.DB.Upsert(ctx, "kb_sources", "conversation_id,turn_index", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to upsert kb_source")
	}
	return rows[0].ID, nil
}

func (k *Knowledge) GetSourcesForConversation(ctx context.Context, conversationID string) ([]KbSource, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,source_type,content,conversation_id,turn_index")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("order", "turn_index.asc")

	var rows []KbSource
	if err := k.DB.Get(ctx, "kb_sources", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (k *Knowledge) SyncConversationSources(ctx context.Context, userID, conversationID string) ([]KbSource, error) {
	turns, err := k.GetConversationTurns(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	for _, turn := range turns {
		_, err := k.UpsertVoiceSource(ctx, struct {
			UserID, ConversationID, UserTranscript, AssistantTranscript string
			TurnIndex                                                   int
		}{
			UserID:              userID,
			ConversationID:      conversationID,
			TurnIndex:           turn.TurnIndex,
			UserTranscript:      turn.UserTranscript,
			AssistantTranscript: turn.AssistantTranscript,
		})
		if err != nil {
			return nil, err
		}
	}
	return k.GetSourcesForConversation(ctx, conversationID)
}

func (k *Knowledge) IsConversationCompiled(ctx context.Context, conversationID string) (bool, error) {
	q := url.Values{}
	q.Set("select", "id,turns_count")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("status", "eq.completed")
	q.Set("order", "created_at.desc")
	q.Set("limit", "1")

	var rows []struct {
		ID         string `json:"id"`
		TurnsCount *int   `json:"turns_count"`
	}
	if err := k.DB.Get(ctx, "kb_compile_log", q, &rows); err != nil {
		log.Warn("failed to check compile status", map[string]any{"error": err.Error()})
		return false, nil
	}
	if len(rows) == 0 {
		return false, nil
	}
	return rows[0].TurnsCount != nil && *rows[0].TurnsCount > 0, nil
}

func (k *Knowledge) CreateCompileLog(ctx context.Context, userID, conversationID string) (string, error) {
	var rows []struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"user_id":         userID,
		"conversation_id": conversationID,
		"status":          "running",
	}
	if err := k.DB.Insert(ctx, "kb_compile_log", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to create compile log")
	}
	return rows[0].ID, nil
}

func (k *Knowledge) CompleteCompileLog(ctx context.Context, logID, status string, turnsCount, factsAdded int, errMsg string) {
	q := url.Values{}
	q.Set("id", "eq."+logID)
	body := map[string]any{
		"status":       status,
		"turns_count":  turnsCount,
		"facts_added":  factsAdded,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
	}
	if errMsg != "" {
		body["error"] = errMsg
	} else {
		body["error"] = nil
	}
	if err := k.DB.Patch(ctx, "kb_compile_log", q, body); err != nil {
		log.Warn("failed to update compile log", map[string]any{"error": err.Error()})
	}
}

func (k *Knowledge) InsertAssetSource(ctx context.Context, userID, content string, metadata map[string]any) (string, error) {
	var rows []struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"user_id":     userID,
		"source_type": "document",
		"content":     content,
		"metadata":    metadata,
	}
	if err := k.DB.Insert(ctx, "kb_sources", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to insert asset source")
	}
	return rows[0].ID, nil
}

func (k *Knowledge) GetSourceByID(ctx context.Context, sourceID string) (KbSource, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,source_type,content,conversation_id,turn_index,metadata")
	q.Set("id", "eq."+sourceID)

	var rows []KbSource
	if err := k.DB.Get(ctx, "kb_sources", q, &rows); err != nil {
		return KbSource{}, err
	}
	if len(rows) == 0 {
		return KbSource{}, fmt.Errorf("source not found")
	}
	return rows[0], nil
}

func (k *Knowledge) IsSourceCompiled(ctx context.Context, sourceID string) (bool, error) {
	q := url.Values{}
	q.Set("select", "id")
	q.Set("source_id", "eq."+sourceID)
	q.Set("active", "eq.true")
	count, err := k.DB.Count(ctx, "kb_facts", q)
	if err != nil {
		log.Warn("failed to check source facts", map[string]any{"sourceId": sourceID, "error": err.Error()})
		return false, nil
	}
	return count > 0, nil
}

func (k *Knowledge) MarkSourceCompiled(ctx context.Context, sourceID string) error {
	source, err := k.GetSourceByID(ctx, sourceID)
	if err != nil {
		return err
	}
	metadata := map[string]any{}
	for k, v := range source.Metadata {
		metadata[k] = v
	}
	metadata["compiled_at"] = time.Now().UTC().Format(time.RFC3339)

	q := url.Values{}
	q.Set("id", "eq."+sourceID)
	return k.DB.Patch(ctx, "kb_sources", q, map[string]any{"metadata": metadata})
}

func (k *Knowledge) UploadAssetFile(ctx context.Context, userID, filename string, data []byte, mimeType string) (string, error) {
	safeName := regexp.MustCompile(`[^a-zA-Z0-9._-]`).ReplaceAllString(filename, "_")
	path := fmt.Sprintf("%s/%d-%s", userID, time.Now().UnixMilli(), safeName)
	if err := k.DB.UploadStorage(ctx, knowledgeBucket, path, mimeType, data); err != nil {
		return "", err
	}
	return path, nil
}
