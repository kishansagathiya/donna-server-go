package storage

import (
	"context"
	"fmt"
	"net/url"
)

// NotesMemoryV2Overrides holds per-user nullable overrides (nil = inherit server default).
type NotesMemoryV2Overrides struct {
	NotesFeed         *bool `json:"flag_notes_v2_feed"`
	SmartTagging      *bool `json:"flag_notes_v2_smart_tagging"`
	MemoryExtraction  *bool `json:"flag_memory_v2_extraction"`
	MemoryRetrieval   *bool `json:"flag_memory_v2_retrieval"`
}

type FeatureFlags struct {
	DB      *Supabase
	Enabled bool
}

func (f *FeatureFlags) GetNotesMemoryV2Overrides(ctx context.Context, userID string) (NotesMemoryV2Overrides, error) {
	if f == nil || !f.Enabled || f.DB == nil {
		return NotesMemoryV2Overrides{}, nil
	}
	q := url.Values{}
	q.Set("select", "flag_notes_v2_feed,flag_notes_v2_smart_tagging,flag_memory_v2_extraction,flag_memory_v2_retrieval")
	q.Set("user_id", "eq."+userID)

	var rows []NotesMemoryV2Overrides
	if err := f.DB.Get(ctx, "user_preferences", q, &rows); err != nil {
		return NotesMemoryV2Overrides{}, err
	}
	if len(rows) == 0 {
		return NotesMemoryV2Overrides{}, nil
	}
	return rows[0], nil
}

func (f *FeatureFlags) SetNotesMemoryV2Overrides(ctx context.Context, userID string, overrides NotesMemoryV2Overrides) error {
	if f == nil || !f.Enabled || f.DB == nil {
		return fmt.Errorf("feature flags unavailable")
	}
	body := map[string]any{"user_id": userID}
	if overrides.NotesFeed != nil {
		body["flag_notes_v2_feed"] = *overrides.NotesFeed
	}
	if overrides.SmartTagging != nil {
		body["flag_notes_v2_smart_tagging"] = *overrides.SmartTagging
	}
	if overrides.MemoryExtraction != nil {
		body["flag_memory_v2_extraction"] = *overrides.MemoryExtraction
	}
	if overrides.MemoryRetrieval != nil {
		body["flag_memory_v2_retrieval"] = *overrides.MemoryRetrieval
	}
	return f.DB.Upsert(ctx, "user_preferences", "user_id", body, nil)
}
