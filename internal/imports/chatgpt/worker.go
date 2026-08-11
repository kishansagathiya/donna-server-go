package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/notes"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	providerChatGPT       = "chatgpt"
	maxNoteRunes          = 100_000
	maxExtractRunes       = 20_000
	checkpointEvery       = 10
	softDeadlineFraction  = 0.7
	jobWorkTimeout        = 4 * time.Minute
)

// MemoryEnqueuer schedules Memory V2 extraction for imported sources/notes.
type MemoryEnqueuer interface {
	EnqueueFromSource(ctx context.Context, userID, sourceID, content string)
	EnqueueFromNote(ctx context.Context, userID, noteID, content string, contentVersion int64)
}

// Worker processes chatgpt_export_import background jobs.
type Worker struct {
	Imports *storage.ChatGPTImports
	KB      *storage.Knowledge
	Notes   *notes.Sync
	Jobs    *storage.BackgroundJobs
	Memory  MemoryEnqueuer
	DB      *storage.Supabase
}

type jobPayload struct {
	ImportID string `json:"import_id"`
	Cursor   int    `json:"cursor"`
}

// HandleJob downloads the export ZIP, imports conversations/memories, and
// checkpoints progress so large archives can span multiple job leases.
func (w *Worker) HandleJob(parent context.Context, job storage.BackgroundJob) error {
	_ = parent // claim tick context is short; use a detached work context.
	ctx, cancel := context.WithTimeout(context.Background(), jobWorkTimeout)
	defer cancel()

	var payload jobPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("invalid job payload: %w", err)
		}
	}
	if payload.ImportID == "" && job.TargetID != nil {
		payload.ImportID = *job.TargetID
	}
	if payload.ImportID == "" {
		return fmt.Errorf("missing import_id")
	}

	imp, err := w.Imports.GetByIDAdmin(ctx, payload.ImportID)
	if err != nil {
		return err
	}
	if imp.Status == storage.ChatGPTImportCompleted {
		return nil
	}
	if imp.StoragePath == nil || strings.TrimSpace(*imp.StoragePath) == "" {
		return w.failImport(ctx, imp.ID, "missing storage_path")
	}

	running := storage.ChatGPTImportRunning
	started := time.Now().UTC().Format(time.RFC3339Nano)
	clearErr := true
	if _, err := w.Imports.Patch(ctx, imp.ID, storage.ChatGPTImportPatch{
		Status:     &running,
		StartedAt:  &started,
		ClearError: clearErr,
	}); err != nil {
		return err
	}

	data, err := w.DB.DownloadStorageLarge(ctx, storage.ChatGPTImportsBucket, *imp.StoragePath, 15*time.Minute)
	if err != nil {
		return w.failImport(ctx, imp.ID, "download failed: "+err.Error())
	}
	bytesLen := int64(len(data))

	export, err := ParseExportZIP(data)
	if err != nil {
		return w.failImport(ctx, imp.ID, err.Error())
	}

	total := len(export.Conversations)
	cursor := payload.Cursor
	if cursor < imp.CursorIndex {
		cursor = imp.CursorIndex
	}
	if _, err := w.Imports.Patch(ctx, imp.ID, storage.ChatGPTImportPatch{
		Bytes:              &bytesLen,
		ConversationsTotal: &total,
		CursorIndex:        &cursor,
	}); err != nil {
		return err
	}

	deadline := time.Now().Add(time.Duration(float64(jobWorkTimeout) * softDeadlineFraction))
	processed := imp.ConversationsProcessed
	if processed < cursor {
		processed = cursor
	}
	memoriesImported := imp.MemoriesImported

	// Import ChatGPT memory.json once at the start of the first batch.
	if cursor == 0 && len(export.Memories) > 0 && memoriesImported == 0 {
		n, err := w.importMemories(ctx, imp.UserID, imp.ID, export.Memories)
		if err != nil {
			log.Warn("chatgpt memory.json import failed", map[string]any{
				"importId": log.ShortID(imp.ID),
				"error":    err.Error(),
			})
		} else {
			memoriesImported = n
			_ = w.patchProgress(ctx, imp.ID, processed, memoriesImported, cursor)
		}
	}

	for i := cursor; i < total; i++ {
		if err := ctx.Err(); err != nil {
			return w.requeue(ctx, imp, i, processed, memoriesImported, err.Error())
		}
		c := export.Conversations[i]
		if err := w.importConversation(ctx, imp.UserID, imp.ID, c); err != nil {
			log.Warn("chatgpt conversation import failed", map[string]any{
				"importId":       log.ShortID(imp.ID),
				"conversationId": c.ID,
				"error":          err.Error(),
			})
			// Continue; one bad conversation should not fail the whole import.
		}
		processed = i + 1
		cursor = i + 1
		if processed%checkpointEvery == 0 || i == total-1 {
			if err := w.patchProgress(ctx, imp.ID, processed, memoriesImported, cursor); err != nil {
				return err
			}
		}
		if time.Now().After(deadline) && i+1 < total {
			return w.requeue(ctx, imp, cursor, processed, memoriesImported, "")
		}
	}

	completed := storage.ChatGPTImportCompleted
	finished := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = w.Imports.Patch(ctx, imp.ID, storage.ChatGPTImportPatch{
		Status:                 &completed,
		ConversationsProcessed: &processed,
		MemoriesImported:       &memoriesImported,
		CursorIndex:            &cursor,
		FinishedAt:             &finished,
		ClearError:             true,
	})
	return err
}

func (w *Worker) patchProgress(ctx context.Context, importID string, processed, memories, cursor int) error {
	_, err := w.Imports.Patch(ctx, importID, storage.ChatGPTImportPatch{
		ConversationsProcessed: &processed,
		MemoriesImported:       &memories,
		CursorIndex:            &cursor,
	})
	return err
}

func (w *Worker) requeue(ctx context.Context, imp storage.ChatGPTImport, cursor, processed, memories int, lastErr string) error {
	if err := w.patchProgress(ctx, imp.ID, processed, memories, cursor); err != nil {
		return err
	}
	queued := storage.ChatGPTImportQueued
	patch := storage.ChatGPTImportPatch{Status: &queued}
	if lastErr != "" {
		patch.Error = &lastErr
	}
	if _, err := w.Imports.Patch(ctx, imp.ID, patch); err != nil {
		return err
	}
	if w.Jobs == nil || !w.Jobs.Enabled {
		return fmt.Errorf("background jobs unavailable for requeue")
	}
	key := fmt.Sprintf("chatgpt_export_import:%s:%d", imp.ID, cursor)
	_, err := w.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
		UserID:     imp.UserID,
		JobType:    storage.JobTypeChatGPTExportImport,
		DedupeKey:  key,
		Payload:    map[string]any{"import_id": imp.ID, "cursor": cursor},
		TargetKind: storage.TargetKindImport,
		TargetID:   imp.ID,
	})
	return err
}

func (w *Worker) failImport(ctx context.Context, importID, msg string) error {
	failed := storage.ChatGPTImportFailed
	finished := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = w.Imports.Patch(ctx, importID, storage.ChatGPTImportPatch{
		Status:     &failed,
		Error:      &msg,
		FinishedAt: &finished,
	})
	return fmt.Errorf("%s", msg)
}

func (w *Worker) importConversation(ctx context.Context, userID, importID string, c Conversation) error {
	if c.ID == "" || len(c.Messages) == 0 {
		return nil
	}
	existing, err := w.KB.FindChatGPTConversationSourceID(ctx, userID, c.ID)
	if err != nil {
		return err
	}
	if existing != "" {
		return nil
	}

	body := FormatConversationNote(c)
	noteBody := truncateRunes(body, maxNoteRunes)
	noteDate := c.CreatedAt
	if noteDate.IsZero() {
		noteDate = time.Now().UTC()
	}

	metaBase := map[string]any{
		"provider":                 providerChatGPT,
		"from_chatgpt":             true,
		"chatgpt_conversation_id":  c.ID,
		"chatgpt_import_id":        importID,
		"title":                    c.Title,
	}

	// Primary source (linked to note) — full or truncated conversation text.
	primaryMeta := copyMeta(metaBase)
	primaryMeta["kind"] = "conversation"
	sourceID, err := w.KB.InsertChatGPTSource(ctx, userID, noteBody, primaryMeta)
	if err != nil {
		return err
	}

	// FromSource upserts the note and enqueues Memory V2 extract.
	if w.Notes != nil {
		if err := w.Notes.FromSource(ctx, userID, sourceID, "integration", noteBody, noteDate); err != nil {
			log.Warn("chatgpt note upsert failed", map[string]any{
				"conversationId": c.ID,
				"error":          err.Error(),
			})
		}
	} else if w.Memory != nil {
		extractBody := truncateRunes(preferUserContent(c), maxExtractRunes)
		if extractBody == "" {
			extractBody = truncateRunes(noteBody, maxExtractRunes)
		}
		w.Memory.EnqueueFromSource(ctx, userID, sourceID, extractBody)
	}

	// Extra chunks for retrieval when the conversation exceeds one kb_sources row.
	// Primary source already holds the start of the transcript.
	if utf8.RuneCountInString(body) > connectors.MaxChunkChars {
		chunks := connectors.ChunkTranscript(body)
		for i, chunk := range chunks {
			if i == 0 {
				continue
			}
			cm := copyMeta(metaBase)
			cm["kind"] = "transcript_chunk"
			cm["chunk_index"] = i
			if _, err := w.KB.InsertChatGPTSource(ctx, userID, chunk, cm); err != nil {
				log.Warn("chatgpt chunk insert failed", map[string]any{
					"conversationId": c.ID,
					"chunk":          i,
					"error":          err.Error(),
				})
			}
		}
	}

	return nil
}

func (w *Worker) importMemories(ctx context.Context, userID, importID string, entries []MemoryEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	doc := FormatMemoriesDocument(entries)
	meta := map[string]any{
		"provider":          providerChatGPT,
		"from_chatgpt":      true,
		"chatgpt_import_id": importID,
		"kind":              "memory_json",
		"title":             "ChatGPT saved memories",
	}
	sourceID, err := w.KB.InsertChatGPTSource(ctx, userID, doc, meta)
	if err != nil {
		return 0, err
	}
	if w.Notes != nil {
		_ = w.Notes.FromSource(ctx, userID, sourceID, "integration", doc, time.Now().UTC())
	} else if w.Memory != nil {
		w.Memory.EnqueueFromSource(ctx, userID, sourceID, truncateRunes(doc, maxExtractRunes))
	}
	return len(entries), nil
}

func preferUserContent(c Conversation) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(c.Title)
	b.WriteString("\n\n")
	for _, m := range c.Messages {
		if m.Role != "user" {
			continue
		}
		b.WriteString("User: ")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

func copyMeta(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}
