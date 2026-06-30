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

// chitchatTokens are whole-message greetings/acknowledgements that carry no
// memory-retrieval signal (e.g. "hey", "thanks", "ok", "good morning"). When
// the user's entire (normalized) message is one of these, the legacy FTS
// fallback is skipped to avoid 3-4 needless DB round-trips on the hot path.
// Matching is exact-after-normalization so legitimate short queries like
// "coffee preferences" still retrieve.
var chitchatTokens = map[string]struct{}{
	"hey": {}, "hi": {}, "hello": {}, "yo": {}, "howdy": {}, "sup": {},
	"ok": {}, "okay": {}, "k": {}, "kk": {}, "cool": {}, "nice": {},
	"great": {}, "awesome": {}, "perfect": {}, "sounds good": {}, "got it": {},
	"thanks": {}, "thank you": {}, "thx": {}, "ty": {}, "cheers": {},
	"yeah": {}, "yes": {}, "yep": {}, "yup": {}, "no": {}, "nope": {},
	"lol": {}, "haha": {}, "ha": {}, "lmao": {}, "wow": {}, "ohh": {},
	"hmm": {}, "oh": {}, "ah": {}, "ugh": {}, "right": {}, "sure": {},
	"bye": {}, "goodbye": {}, "gm": {}, "good morning": {}, "good night": {},
	"gn": {}, "what's up": {}, "wassup": {},
}

// looksLikeChitchat reports whether the entire (normalized) message is a
// known low-signal greeting/acknowledgement, in which case memory retrieval
// is skipped. Partial matches are NOT treated as chitchat, so real queries
// always retrieve.
func looksLikeChitchat(transcript string) bool {
	normalized := strings.ToLower(strings.TrimSpace(transcript))
	normalized = strings.TrimSpace(strings.Trim(normalized, "!?.,~"))
	_, ok := chitchatTokens[normalized]
	return ok
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

// defaultMemoryMinScore is used when no threshold is configured (tests, zero value).
const defaultMemoryMinScore = 0.35

func DefaultAugment(ctx context.Context, kb *storage.Knowledge, notes *storage.Notes, transcript, userID, sessionID string, minScore float64) TranscriptAugmentation {
	_ = sessionID
	base := TranscriptAugmentation{Transcript: transcript}

	if looksLikeChitchat(transcript) {
		base.Text = FormatAugmentedUserMessage(base)
		return base
	}

	if minScore <= 0 {
		minScore = defaultMemoryMinScore
	}

	// Preferred path: a single hybrid match_memory RPC that blends vector
	// similarity, FTS, and recency across both facts and notes.
	if kb != nil && kb.Enabled {
		hits, err := kb.RetrieveMemory(ctx, userID, transcript, 20)
		if err == nil {
			base.Retrieved = applyTokenBudget(hits, augmentTokenBudget, minScore)
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
// Hits below minScore are excluded so weakly related memories are not injected.
func applyTokenBudget(hits []storage.MemoryHit, charBudget int, minScore float64) []string {
	out := make([]string, 0, len(hits))
	used := 0
	for _, h := range hits {
		if h.Text == "" {
			continue
		}
		if h.Score < minScore {
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
