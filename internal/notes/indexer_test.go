package notes

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestHeuristicIndex_urgentAndImportant(t *testing.T) {
	got := heuristicIndex("This is urgent and must be done today — critical deadline")
	if !got.IsUrgent {
		t.Fatal("expected urgent")
	}
	if !got.IsImportant {
		t.Fatal("expected important")
	}
}

func TestHeuristicIndex_neutral(t *testing.T) {
	got := heuristicIndex("Had coffee with Alex and talked about weather")
	if got.IsUrgent || got.IsImportant {
		t.Fatalf("expected no flags, got urgent=%v important=%v", got.IsUrgent, got.IsImportant)
	}
}

func TestNoteIDFromJob_targetAndPayload(t *testing.T) {
	id := "note-123"
	job := storage.BackgroundJob{TargetID: &id}
	if got := noteIDFromJob(job); got != id {
		t.Fatalf("target id: got %q", got)
	}

	payload, _ := json.Marshal(map[string]any{"note_id": "from-payload"})
	job = storage.BackgroundJob{Payload: payload}
	if got := noteIDFromJob(job); got != "from-payload" {
		t.Fatalf("payload id: got %q", got)
	}
}

func TestIndexerHandleJob_emptyNoteIDNoops(t *testing.T) {
	idx := &Indexer{}
	if err := idx.HandleJob(context.Background(), storage.BackgroundJob{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
