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

func DefaultAugment(ctx context.Context, kb *storage.Knowledge, transcript, userID, sessionID string) TranscriptAugmentation {
	_ = sessionID
	base := TranscriptAugmentation{Transcript: transcript}

	if kb != nil && kb.Enabled {
		retrieved, err := kb.RetrieveFacts(ctx, userID, transcript, 10)
		if err == nil && len(retrieved) > 0 {
			base.Retrieved = retrieved
		}
	}

	base.Text = FormatAugmentedUserMessage(base)
	return base
}
