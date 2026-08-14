package notes

import (
	"context"
	"errors"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// LinkExpander handles JobTypeNoteLinkExpand: fetch/browse URLs in a note,
// write the page text back, then run the usual enrichment + memory extract.
type LinkExpander struct {
	Sync *Sync
}

func (e *LinkExpander) HandleJob(ctx context.Context, job storage.BackgroundJob) error {
	if e == nil || e.Sync == nil || e.Sync.Store == nil || !e.Sync.Store.Enabled {
		return nil
	}
	userID := ""
	if job.UserID != nil {
		userID = strings.TrimSpace(*job.UserID)
	}
	noteID := noteIDFromJob(job)
	if userID == "" || noteID == "" {
		return nil
	}

	note, err := e.Sync.Store.GetNoteByID(ctx, userID, noteID)
	if err != nil {
		return err
	}

	expanded := ingest.ExpandLinks(note.Content)
	if expanded != note.Content {
		expected := note.ContentVersion
		updated, err := e.Sync.Store.UpdateNote(ctx, userID, noteID, storage.NoteUpdate{
			Content:         &expanded,
			ExpectedVersion: &expected,
		})
		if err != nil {
			if errors.Is(err, storage.ErrVersionConflict) {
				log.Print("note link expand skipped stale note", map[string]any{
					"noteId": log.ShortID(noteID),
				})
				return nil
			}
			return err
		}
		note = updated
	}

	e.Sync.enqueueEnrichment(ctx, userID, note.ID, note.Content, note.ContentVersion)
	if e.Sync.Intents != nil {
		e.Sync.Intents.EnqueueNote(userID, note.ID, note.Content)
	}
	return nil
}
