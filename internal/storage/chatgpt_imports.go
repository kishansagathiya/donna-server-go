package storage

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

const (
	ChatGPTImportsBucket = "chatgpt-imports"

	ChatGPTImportAwaitingUpload = "awaiting_upload"
	ChatGPTImportQueued         = "queued"
	ChatGPTImportRunning        = "running"
	ChatGPTImportCompleted      = "completed"
	ChatGPTImportFailed         = "failed"
)

// ChatGPTImport tracks a single ChatGPT export ZIP upload + processing run.
type ChatGPTImport struct {
	ID                     string  `json:"id"`
	UserID                 string  `json:"user_id"`
	Status                 string  `json:"status"`
	StoragePath            *string `json:"storage_path"`
	Bytes                  *int64  `json:"bytes"`
	ConversationsTotal     int     `json:"conversations_total"`
	ConversationsProcessed int     `json:"conversations_processed"`
	MemoriesImported       int     `json:"memories_imported"`
	CursorIndex            int     `json:"cursor_index"`
	Error                  *string `json:"error"`
	StartedAt              *string `json:"started_at"`
	FinishedAt             *string `json:"finished_at"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              string  `json:"updated_at"`
}

// ChatGPTImports is the store for chatgpt_imports rows.
type ChatGPTImports struct {
	DB      *Supabase
	Enabled bool
}

func (s *ChatGPTImports) selectColumns() string {
	return "id,user_id,status,storage_path,bytes,conversations_total,conversations_processed,memories_imported,cursor_index,error,started_at,finished_at,created_at,updated_at"
}

func (s *ChatGPTImports) Create(ctx context.Context, userID, id, storagePath string) (ChatGPTImport, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return ChatGPTImport{}, fmt.Errorf("chatgpt imports unavailable")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	body := map[string]any{
		"user_id":      userID,
		"status":       ChatGPTImportAwaitingUpload,
		"storage_path": storagePath,
		"created_at":   now,
		"updated_at":   now,
	}
	if id != "" {
		body["id"] = id
	}
	var rows []ChatGPTImport
	if err := s.DB.Insert(ctx, "chatgpt_imports", body, &rows); err != nil {
		return ChatGPTImport{}, err
	}
	if len(rows) == 0 {
		return ChatGPTImport{}, fmt.Errorf("failed to create chatgpt import")
	}
	return rows[0], nil
}

func (s *ChatGPTImports) GetByID(ctx context.Context, userID, id string) (ChatGPTImport, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return ChatGPTImport{}, fmt.Errorf("chatgpt imports unavailable")
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("id", "eq."+id)
	q.Set("user_id", "eq."+userID)
	var rows []ChatGPTImport
	if err := s.DB.Get(ctx, "chatgpt_imports", q, &rows); err != nil {
		return ChatGPTImport{}, err
	}
	if len(rows) == 0 {
		return ChatGPTImport{}, fmt.Errorf("import not found")
	}
	return rows[0], nil
}

func (s *ChatGPTImports) GetByIDAdmin(ctx context.Context, id string) (ChatGPTImport, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return ChatGPTImport{}, fmt.Errorf("chatgpt imports unavailable")
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("id", "eq."+id)
	var rows []ChatGPTImport
	if err := s.DB.Get(ctx, "chatgpt_imports", q, &rows); err != nil {
		return ChatGPTImport{}, err
	}
	if len(rows) == 0 {
		return ChatGPTImport{}, fmt.Errorf("import not found")
	}
	return rows[0], nil
}

func (s *ChatGPTImports) LatestForUser(ctx context.Context, userID string) (*ChatGPTImport, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("chatgpt imports unavailable")
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("order", "created_at.desc")
	q.Set("limit", "1")
	var rows []ChatGPTImport
	if err := s.DB.Get(ctx, "chatgpt_imports", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

type ChatGPTImportPatch struct {
	Status                 *string
	Bytes                  *int64
	ConversationsTotal     *int
	ConversationsProcessed *int
	MemoriesImported       *int
	CursorIndex            *int
	Error                  *string
	ClearError             bool
	StartedAt              *string
	FinishedAt             *string
}

func (s *ChatGPTImports) Patch(ctx context.Context, id string, patch ChatGPTImportPatch) (ChatGPTImport, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return ChatGPTImport{}, fmt.Errorf("chatgpt imports unavailable")
	}
	body := map[string]any{
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if patch.Status != nil {
		body["status"] = *patch.Status
	}
	if patch.Bytes != nil {
		body["bytes"] = *patch.Bytes
	}
	if patch.ConversationsTotal != nil {
		body["conversations_total"] = *patch.ConversationsTotal
	}
	if patch.ConversationsProcessed != nil {
		body["conversations_processed"] = *patch.ConversationsProcessed
	}
	if patch.MemoriesImported != nil {
		body["memories_imported"] = *patch.MemoriesImported
	}
	if patch.CursorIndex != nil {
		body["cursor_index"] = *patch.CursorIndex
	}
	if patch.ClearError {
		body["error"] = nil
	} else if patch.Error != nil {
		body["error"] = *patch.Error
	}
	if patch.StartedAt != nil {
		body["started_at"] = *patch.StartedAt
	}
	if patch.FinishedAt != nil {
		body["finished_at"] = *patch.FinishedAt
	}

	q := url.Values{}
	q.Set("id", "eq."+id)
	var rows []ChatGPTImport
	if err := s.DB.PatchReturning(ctx, "chatgpt_imports", q, body, &rows); err != nil {
		return ChatGPTImport{}, err
	}
	if len(rows) == 0 {
		// Some environments may not return representation; re-fetch.
		return s.GetByIDAdmin(ctx, id)
	}
	return rows[0], nil
}

// FindChatGPTConversationSourceID returns an existing kb_sources id for a
// previously imported ChatGPT conversation (idempotent re-import).
func (k *Knowledge) FindChatGPTConversationSourceID(ctx context.Context, userID, conversationID string) (string, error) {
	if k == nil || !k.Enabled || k.DB == nil || conversationID == "" {
		return "", nil
	}
	q := url.Values{}
	q.Set("select", "id")
	q.Set("user_id", "eq."+userID)
	q.Set("source_type", "eq.integration")
	q.Set("metadata->>chatgpt_conversation_id", "eq."+conversationID)
	q.Set("metadata->>kind", "eq.conversation")
	q.Set("limit", "1")
	var rows []struct {
		ID string `json:"id"`
	}
	if err := k.DB.Get(ctx, "kb_sources", q, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].ID, nil
}

// InsertChatGPTSource inserts a kb_sources row for ChatGPT import content.
func (k *Knowledge) InsertChatGPTSource(ctx context.Context, userID, content string, metadata map[string]any) (string, error) {
	if k == nil || !k.Enabled || k.DB == nil {
		return "", fmt.Errorf("knowledge disabled")
	}
	body := map[string]any{
		"user_id":     userID,
		"source_type": "integration",
		"content":     content,
		"metadata":    metadata,
	}
	if k.Embedder != nil && k.Embedder.Enabled() {
		if vec, err := k.Embedder.EmbedOne(ctx, content); err == nil {
			body["embedding"] = vec
		}
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := k.DB.Insert(ctx, "kb_sources", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to insert chatgpt source")
	}
	return rows[0].ID, nil
}
