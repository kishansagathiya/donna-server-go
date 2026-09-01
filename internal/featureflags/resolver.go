package featureflags

import (
	"context"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// NotesMemoryV2 holds effective flag values for one user.
type NotesMemoryV2 struct {
	NotesFeed        bool `json:"notesFeed"`
	SmartTagging     bool `json:"smartTagging"`
	MemoryExtraction bool `json:"memoryExtraction"`
	MemoryRetrieval  bool `json:"memoryRetrieval"`
	LocalAgentsV1    bool `json:"localAgentsV1"`
}

type Resolver struct {
	Defaults *config.Config
	Store    *storage.FeatureFlags
}

func (r *Resolver) NotesMemoryV2ForUser(ctx context.Context, userID string) (NotesMemoryV2, error) {
	out := NotesMemoryV2{}
	if r != nil && r.Defaults != nil {
		out.NotesFeed = r.Defaults.NotesV2Feed
		out.SmartTagging = r.Defaults.NotesV2SmartTagging
		out.MemoryExtraction = r.Defaults.MemoryV2Extraction
		out.MemoryRetrieval = r.Defaults.MemoryV2Retrieval
		out.LocalAgentsV1 = r.Defaults.LocalAgentsV1
	}
	if r == nil || r.Store == nil || userID == "" {
		return out, nil
	}
	overrides, err := r.Store.GetNotesMemoryV2Overrides(ctx, userID)
	if err != nil {
		return out, err
	}
	if overrides.NotesFeed != nil {
		out.NotesFeed = *overrides.NotesFeed
	}
	if overrides.SmartTagging != nil {
		out.SmartTagging = *overrides.SmartTagging
	}
	if overrides.MemoryExtraction != nil {
		out.MemoryExtraction = *overrides.MemoryExtraction
	}
	if overrides.MemoryRetrieval != nil {
		out.MemoryRetrieval = *overrides.MemoryRetrieval
	}
	if overrides.LocalAgentsV1 != nil {
		out.LocalAgentsV1 = *overrides.LocalAgentsV1
	}
	return out, nil
}
