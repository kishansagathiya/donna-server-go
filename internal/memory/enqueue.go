package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Enqueuer schedules memory_extract jobs for newly written content.
type Enqueuer struct {
	Jobs  *storage.BackgroundJobs
	Flags *featureflags.Resolver
}

// EnqueueFromNote schedules extraction for a note create/edit (new data only).
func (e *Enqueuer) EnqueueFromNote(ctx context.Context, userID, noteID, content string, contentVersion int64) {
	if e == nil || e.Jobs == nil || !e.Jobs.Enabled || userID == "" || noteID == "" {
		return
	}
	if !e.extractionEnabled(ctx, userID) {
		return
	}
	if contentVersion <= 0 {
		contentVersion = 1
	}
	key := fmt.Sprintf("memory_extract:note:%s:%d", noteID, contentVersion)
	payload := map[string]any{
		"source_kind": storage.EvidenceNote,
		"source_id":   noteID,
		"note_id":     noteID,
		"content":     content,
		"excerpt":     truncate(content, 500),
	}
	if _, err := e.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
		UserID:        userID,
		JobType:       storage.JobTypeMemoryExtract,
		DedupeKey:     key,
		Payload:       payload,
		TargetKind:    storage.TargetKindNote,
		TargetID:      noteID,
		TargetVersion: contentVersion,
	}); err != nil {
		log.Warn("memory extract enqueue failed", map[string]any{
			"noteId": log.ShortID(noteID),
			"error":  err.Error(),
		})
	}
}

// EnqueueFromConversationTurn schedules extraction for a new chat/voice turn.
func (e *Enqueuer) EnqueueFromConversationTurn(ctx context.Context, userID, conversationID string, turnIndex int, transcript string) {
	if e == nil || e.Jobs == nil || !e.Jobs.Enabled || userID == "" {
		return
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" || conversationID == "" {
		return
	}
	if !e.extractionEnabled(ctx, userID) {
		return
	}
	key := fmt.Sprintf("memory_extract:turn:%s:%d", conversationID, turnIndex)
	payload := map[string]any{
		"source_kind":     storage.EvidenceConversationTurn,
		"source_id":       conversationID,
		"conversation_id": conversationID,
		"turn_index":      turnIndex,
		"content":         "User: " + transcript,
		"excerpt":         truncate(transcript, 500),
	}
	if _, err := e.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
		UserID:    userID,
		JobType:   storage.JobTypeMemoryExtract,
		DedupeKey: key,
		Payload:   payload,
		TargetKind: storage.TargetKindConversation,
		TargetID:  conversationID,
	}); err != nil {
		log.Warn("memory extract enqueue failed", map[string]any{
			"conversationId": log.ShortID(conversationID),
			"error":          err.Error(),
		})
	}
}

// EnqueueFromSource schedules extraction for a newly ingested kb source.
func (e *Enqueuer) EnqueueFromSource(ctx context.Context, userID, sourceID, content string) {
	if e == nil || e.Jobs == nil || !e.Jobs.Enabled || userID == "" || sourceID == "" {
		return
	}
	if !e.extractionEnabled(ctx, userID) {
		return
	}
	key := fmt.Sprintf("memory_extract:source:%s", sourceID)
	payload := map[string]any{
		"source_kind": storage.EvidenceKBSource,
		"source_id":   sourceID,
		"content":     content,
		"excerpt":     truncate(content, 500),
	}
	if _, err := e.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
		UserID:     userID,
		JobType:    storage.JobTypeMemoryExtract,
		DedupeKey:  key,
		Payload:    payload,
		TargetKind: storage.TargetKindSource,
		TargetID:   sourceID,
	}); err != nil {
		log.Warn("memory extract enqueue failed", map[string]any{
			"sourceId": log.ShortID(sourceID),
			"error":    err.Error(),
		})
	}
}

func (e *Enqueuer) extractionEnabled(ctx context.Context, userID string) bool {
	if e.Flags == nil {
		return false
	}
	flags, err := e.Flags.NotesMemoryV2ForUser(ctx, userID)
	if err != nil {
		log.Warn("memory extract flag lookup failed", map[string]any{"error": err.Error()})
		return false
	}
	return flags.MemoryExtraction
}
