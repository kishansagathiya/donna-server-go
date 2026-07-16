package notes

import (
	"context"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Sync struct {
	Store   *storage.Notes
	Queue   *IndexQueue
	Intents IntentQueue
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
	if s.Queue != nil {
		s.Queue.Enqueue(noteID)
	}

	if tags := storage.ExtractHashtags(content); len(tags) > 0 {
		if _, err := s.Store.SetTagsForNote(ctx, userID, noteID, tags); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sync) FromVoiceSources(ctx context.Context, userID string, sources []storage.KbSource) error {
	if s.Store == nil || !s.Store.Enabled {
		return nil
	}

	for _, source := range sources {
		if source.SourceType != "voice_turn" {
			continue
		}
		content := ExtractVoiceUserContent(source.Content)
		if strings.TrimSpace(content) == "" {
			continue
		}

		noteDate := time.Now().UTC()
		if source.CreatedAt != "" {
			if parsed, err := time.Parse(time.RFC3339, source.CreatedAt); err == nil {
				noteDate = parsed.UTC()
			}
		}

		if err := s.FromSource(ctx, userID, source.ID, "voice_turn", content, noteDate); err != nil {
			return err
		}
	}
	return nil
}

// CreateManual commits a user-authored note from any channel: typed via the
// Notes tab, the chat `notes` mode shortcut, or dictated over the /voice
// WebSocket. audio is non-nil only for the voice-dictation flow; for typed
// channels it stays nil so the row remains a plain text note.
func (s *Sync) CreateManual(ctx context.Context, userID, content string, noteDate *time.Time, audio *storage.NoteAudioInput) (storage.Note, error) {
	note, err := s.Store.CreateNote(ctx, userID, "manual", content, storage.CreateNoteOptions{
		NoteDate: noteDate,
		Audio:    audio,
	})
	if err != nil {
		return storage.Note{}, err
	}
	if s.Queue != nil {
		s.Queue.Enqueue(note.ID)
	}
	if s.Intents != nil {
		s.Intents.EnqueueNote(userID, note.ID, content)
	}
	if tags := storage.ExtractHashtags(content); len(tags) > 0 {
		if _, err := s.Store.SetTagsForNote(ctx, userID, note.ID, tags); err != nil {
			return storage.Note{}, err
		}
	}
	return note, nil
}
