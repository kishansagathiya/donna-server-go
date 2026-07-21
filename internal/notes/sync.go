package notes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Sync struct {
	Store   *storage.Notes
	Queue   *IndexQueue
	Jobs    *storage.BackgroundJobs
	Intents IntentQueue
	Flags   *featureflags.Resolver
	Memory  MemoryExtractEnqueuer
}

// MemoryExtractEnqueuer schedules structured memory extraction for new notes.
type MemoryExtractEnqueuer interface {
	EnqueueFromNote(ctx context.Context, userID, noteID, content string, contentVersion int64)
}

// IntentQueue extracts actionable intents from user-authored notes.
type IntentQueue interface {
	EnqueueNote(userID, noteID, content string)
}

func (s *Sync) FromSource(ctx context.Context, userID, sourceID, sourceType, content string, noteDate time.Time) error {
	if s.Store == nil || !s.Store.Enabled {
		return nil
	}

	noteID, err := s.Store.UpsertNoteFromSource(ctx, userID, sourceID, sourceType, content, noteDate)
	if err != nil {
		return err
	}
	s.enqueueEnrichment(ctx, userID, noteID, content, 1)

	if tags := storage.ExtractHashtags(content); len(tags) > 0 {
		if _, err := s.Store.SetLockedTagsForNote(ctx, userID, noteID, tags, "hashtag"); err != nil {
			return err
		}
	}
	return nil
}

// FromVoiceSources previously created one curated note per talk voice turn.
// Notes V2 keeps voice turns in conversation/kb_sources only — curated Notes
// come from explicit capture (manual, notes-mode dictation, excerpts).
func (s *Sync) FromVoiceSources(ctx context.Context, userID string, sources []storage.KbSource) error {
	_ = ctx
	_ = userID
	_ = sources
	return nil
}

// CreateManual commits a user-authored note from any channel: typed via the
// Notes tab, the chat `notes` mode shortcut, or dictated over the /voice
// WebSocket. audio is non-nil only for the voice-dictation flow; for typed
// channels it stays nil so the row remains a plain text note.
// clientID, when non-empty, is used for optimistic/idempotent creates.
func (s *Sync) CreateManual(ctx context.Context, userID, content string, noteDate *time.Time, audio *storage.NoteAudioInput) (storage.Note, error) {
	return s.CreateManualWithID(ctx, userID, "", content, noteDate, audio)
}

func (s *Sync) CreateManualWithID(ctx context.Context, userID, clientID, content string, noteDate *time.Time, audio *storage.NoteAudioInput) (storage.Note, error) {
	note, err := s.Store.CreateNote(ctx, userID, "manual", content, storage.CreateNoteOptions{
		ID:       strings.TrimSpace(clientID),
		NoteDate: noteDate,
		Audio:    audio,
	})
	if err != nil {
		return storage.Note{}, err
	}
	s.enqueueEnrichment(ctx, userID, note.ID, content, note.ContentVersion)
	if s.Intents != nil {
		s.Intents.EnqueueNote(userID, note.ID, content)
	}
	if tags := storage.ExtractHashtags(content); len(tags) > 0 {
		if _, err := s.Store.SetLockedTagsForNote(ctx, userID, note.ID, tags, "hashtag"); err != nil {
			return storage.Note{}, err
		}
	}
	return note, nil
}

// enqueueEnrichment persists first, then schedules durable background jobs.
// No synchronous LLM or embedding work runs here.
func (s *Sync) enqueueEnrichment(ctx context.Context, userID, noteID, content string, contentVersion int64) {
	if noteID == "" {
		return
	}
	if s.Jobs == nil || !s.Jobs.Enabled {
		return
	}
	if contentVersion <= 0 {
		contentVersion = 1
	}

	enrichKey := fmt.Sprintf("note_enrich:%s:%d", noteID, contentVersion)
	embedKey := fmt.Sprintf("note_embed:%s:%d", noteID, contentVersion)
	payload := map[string]any{"note_id": noteID}

	if _, err := s.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
		UserID:        userID,
		JobType:       storage.JobTypeNoteEnrich,
		DedupeKey:     enrichKey,
		Payload:       payload,
		TargetKind:    storage.TargetKindNote,
		TargetID:      noteID,
		TargetVersion: contentVersion,
	}); err != nil {
		log.Warn("note enrich job enqueue failed", map[string]any{
			"noteId": log.ShortID(noteID),
			"error":  err.Error(),
		})
		return
	}
	if _, err := s.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
		UserID:        userID,
		JobType:       storage.JobTypeNoteEmbed,
		DedupeKey:     embedKey,
		Payload:       payload,
		TargetKind:    storage.TargetKindNote,
		TargetID:      noteID,
		TargetVersion: contentVersion,
	}); err != nil {
		log.Warn("note embed job enqueue failed", map[string]any{
			"noteId": log.ShortID(noteID),
			"error":  err.Error(),
		})
		return
	}

	smartTagging := false
	if s.Flags != nil {
		if flags, err := s.Flags.NotesMemoryV2ForUser(ctx, userID); err == nil {
			smartTagging = flags.SmartTagging
		}
	}
	if smartTagging {
		tagKey := fmt.Sprintf("smart_tag_enrich:%s:%d", noteID, contentVersion)
		if _, err := s.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
			UserID:        userID,
			JobType:       storage.JobTypeSmartTagEnrich,
			DedupeKey:     tagKey,
			Payload:       payload,
			TargetKind:    storage.TargetKindNote,
			TargetID:      noteID,
			TargetVersion: contentVersion,
		}); err != nil {
			log.Warn("smart tag enrich job enqueue failed", map[string]any{
				"noteId": log.ShortID(noteID),
				"error":  err.Error(),
			})
		}
	}

	if s.Memory != nil {
		s.Memory.EnqueueFromNote(ctx, userID, noteID, content, contentVersion)
	}

	if err := s.Store.MarkEnrichmentQueued(ctx, userID, noteID); err != nil {
		log.Warn("mark enrichment queued failed", map[string]any{
			"noteId": log.ShortID(noteID),
			"error":  err.Error(),
		})
	}
}
