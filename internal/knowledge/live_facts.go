package knowledge

import (
	"context"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// LiveFactsInput carries transcript data for immediate knowledge persistence.
type LiveFactsInput struct {
	UserID         string
	Transcript     string
	ConversationID string
	TurnIndex      int
}

// PersistLiveFactsFromTranscript extracts high-confidence facts (e.g. name) from a
// live voice turn and writes them to Supabase immediately, without waiting for
// post-session compilation.
func PersistLiveFactsFromTranscript(ctx context.Context, kb *storage.Knowledge, input LiveFactsInput) {
	if kb == nil || !kb.Enabled {
		return
	}
	transcript := strings.TrimSpace(input.Transcript)
	if transcript == "" || strings.TrimSpace(input.UserID) == "" {
		return
	}

	content := "User: " + transcript
	sources := []SourceSlice{{Content: content}}
	if input.ConversationID != "" {
		sources[0].TurnIndex = &input.TurnIndex
	}

	obvious := ExtractObviousFacts(sources)
	if len(obvious) == 0 {
		return
	}

	existingFacts, err := kb.GetActiveFacts(ctx, input.UserID)
	if err != nil {
		log.Warn("live facts: failed to load existing facts", map[string]any{
			"user":  shortID(input.UserID),
			"error": err.Error(),
		})
		return
	}

	existingKeys := make(map[string]struct{}, len(existingFacts))
	for _, f := range existingFacts {
		existingKeys[strings.ToLower(f.Fact)] = struct{}{}
	}

	deduped := make([]storage.NewFactInput, 0, len(obvious))
	for _, f := range obvious {
		if _, ok := existingKeys[strings.ToLower(f.Fact)]; ok {
			continue
		}
		deduped = append(deduped, f)
	}

	if len(deduped) == 0 {
		return
	}

	added, err := kb.InsertFacts(ctx, input.UserID, deduped)
	if err != nil {
		log.Warn("live facts: failed to insert facts", map[string]any{
			"user":  shortID(input.UserID),
			"error": err.Error(),
		})
		return
	}

	var nameToMerge string
	for _, f := range deduped {
		if f.Topic != nil && *f.Topic == "identity" && f.EntityName != nil {
			nameToMerge = *f.EntityName
			break
		}
	}
	if nameToMerge != "" {
		identityFact := "The user's name is " + nameToMerge + "."
		if err := kb.AddIdentityFact(ctx, input.UserID, identityFact); err != nil {
			log.Warn("live facts: failed to record identity fact", map[string]any{
				"user":  shortID(input.UserID),
				"error": err.Error(),
			})
		}
	}

	kb.LogKnowledge("live facts persisted", map[string]any{
		"user":       shortID(input.UserID),
		"factsAdded": added,
	})
}

// PersistLiveFactsAsync runs PersistLiveFactsFromTranscript in a background goroutine.
func PersistLiveFactsAsync(kb *storage.Knowledge, input LiveFactsInput) {
	if kb == nil || !kb.Enabled {
		return
	}
	go func() {
		PersistLiveFactsFromTranscript(context.Background(), kb, input)
	}()
}
