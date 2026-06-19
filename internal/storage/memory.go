package storage

import (
	"context"
	"net/url"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

// MemoryHit is a single blended retrieval result from match_memory.
type MemoryHit struct {
	Source string  `json:"source"` // "fact" | "note"
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	Score  float64 `json:"score"`
}

// RetrieveMemory calls the match_memory RPC: it embeds the transcript, then
// blends vector similarity, FTS, and recency server-side across both kb_facts
// and notes. Returns an error if the embedder is unavailable or the RPC fails
// so the caller can fall back to the legacy FTS path.
func (k *Knowledge) RetrieveMemory(ctx context.Context, userID, transcript string, limit int) ([]MemoryHit, error) {
	if !k.Enabled {
		return nil, nil
	}
	if k.Embedder == nil || !k.Embedder.Enabled() {
		return nil, errEmbedderUnavailable
	}
	if strings.TrimSpace(transcript) == "" {
		return nil, nil
	}

	embedding, err := k.Embedder.EmbedOne(ctx, transcript)
	if err != nil {
		log.Warn("retrieve memory: embedding failed", map[string]any{
			"userId": log.ShortID(userID),
			"error":  err.Error(),
		})
		return nil, err
	}

	body := map[string]any{
		"p_user_id":         userID,
		"p_query_embedding": embedding,
		"p_query_text":      transcript,
		"p_limit":           limit,
	}

	var hits []MemoryHit
	if err := k.DB.RPC(ctx, "match_memory", body, &hits); err != nil {
		log.Warn("retrieve memory: RPC failed", map[string]any{
			"userId": log.ShortID(userID),
			"error":  err.Error(),
		})
		return nil, err
	}
	return hits, nil
}

var errEmbedderUnavailable = &memoryErr{"embedder unavailable"}

type memoryErr struct{ msg string }

func (e *memoryErr) Error() string { return e.msg }

// appendIdentityFacts loads the user's machine-detected identity_facts and
// prepends them to the given summary. Used at read time so the LLM-generated
// summary can never silently drop the user's name.
func (k *Knowledge) appendIdentityFacts(ctx context.Context, userID, summary string) string {
	if !k.Enabled {
		return summary
	}
	q := url.Values{}
	q.Set("select", "identity_facts")
	q.Set("user_id", "eq."+userID)

	var rows []struct {
		IdentityFacts []string `json:"identity_facts"`
	}
	if err := k.DB.Get(ctx, "kb_user_profiles", q, &rows); err != nil {
		return summary
	}
	if len(rows) == 0 || len(rows[0].IdentityFacts) == 0 {
		return summary
	}
	prefix := strings.Join(rows[0].IdentityFacts, " ")
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return prefix
	}
	return prefix + " " + summary
}
