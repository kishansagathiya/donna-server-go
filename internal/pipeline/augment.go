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

func DefaultAugment(ctx context.Context, kb *storage.Knowledge, notes *storage.Notes, transcript, userID, sessionID string) TranscriptAugmentation {
	_ = sessionID
	base := TranscriptAugmentation{Transcript: transcript}

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
