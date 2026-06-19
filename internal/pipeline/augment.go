package pipeline

import (
	"context"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type TranscriptAugmentation struct {
	Transcript   string
	Text         string
	Retrieved    []string
	SessionNotes string
}

func FormatAugmentedUserMessage(augmented TranscriptAugmentation) string {
	var parts []string
	if len(augmented.Retrieved) > 0 {
		parts = append(parts, "[Retrieved: "+strings.Join(augmented.Retrieved, " | ")+"]")
	}
	if augmented.SessionNotes != "" {
		parts = append(parts, "[Session: "+augmented.SessionNotes+"]")
	}
	parts = append(parts, fmtQuoteUser(augmented.Transcript))
	return strings.Join(parts, "\n")
}

func fmtQuoteUser(transcript string) string {
	return `User said: "` + transcript + `"`
}

// augmentTokenBudget is the soft character cap (~1500 tokens ≈ 6000 chars) for
// retrieved memory injected into the prompt. Lowest-scoring items are dropped
// from the tail when the budget is exceeded.
const augmentTokenBudget = 6000

func DefaultAugment(ctx context.Context, kb *storage.Knowledge, notes *storage.Notes, transcript, userID, sessionID string) TranscriptAugmentation {
	_ = sessionID
	base := TranscriptAugmentation{Transcript: transcript}

	// Preferred path: a single hybrid match_memory RPC that blends vector
	// similarity, FTS, and recency across both facts and notes.
	if kb != nil && kb.Enabled {
		hits, err := kb.RetrieveMemory(ctx, userID, transcript, 20)
		if err == nil {
			base.Retrieved = applyTokenBudget(hits, augmentTokenBudget)
			base.Text = FormatAugmentedUserMessage(base)
			return base
		}
	}

	// Graceful degradation: legacy FTS + recency path (no embeddings/RPC).
	var retrieved []string
	seen := make(map[string]struct{})

	appendRetrieved := func(items []string) {
		for _, item := range items {
			key := strings.ToLower(strings.TrimSpace(item))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			retrieved = append(retrieved, item)
		}
	}

	if notes != nil && notes.Enabled {
		noteSnippets, err := notes.RetrieveNoteSnippets(ctx, userID, transcript, 6)
		if err == nil {
			appendRetrieved(noteSnippets)
		}
	}

	if kb != nil && kb.Enabled {
		facts, err := kb.RetrieveFacts(ctx, userID, transcript, 10)
		if err == nil {
			appendRetrieved(facts)
		}
	}

	if len(retrieved) > 10 {
		retrieved = retrieved[:10]
	}
	base.Retrieved = retrieved

	base.Text = FormatAugmentedUserMessage(base)
	return base
}

// applyTokenBudget trims the retrieved list to fit within charBudget, dropping
// lowest-scoring items from the tail (hits are already score-descending).
func applyTokenBudget(hits []storage.MemoryHit, charBudget int) []string {
	out := make([]string, 0, len(hits))
	used := 0
	for _, h := range hits {
		if h.Text == "" {
			continue
		}
		if used+len(h.Text) > charBudget && len(out) > 0 {
			break
		}
		out = append(out, h.Text)
		used += len(h.Text) + 3 // account for " | " separator
	}
	return out
}
