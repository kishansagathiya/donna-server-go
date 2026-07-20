package featureflags

import (
	"context"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
)

func TestNotesMemoryV2ForUserDefaults(t *testing.T) {
	r := &Resolver{
		Defaults: &config.Config{
			NotesV2Feed:         true,
			NotesV2SmartTagging: false,
			MemoryV2Extraction: true,
			MemoryV2Retrieval:   true,
		},
	}
	got, err := r.NotesMemoryV2ForUser(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.NotesFeed || got.SmartTagging || !got.MemoryExtraction || !got.MemoryRetrieval {
		t.Fatalf("unexpected flags: %+v", got)
	}
}
