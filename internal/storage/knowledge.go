package storage

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

type Knowledge struct {
	DB      *Supabase
	Enabled bool
}

type factRow struct {
	Fact       string  `json:"fact"`
	EntityName *string `json:"entity_name"`
	Topic      *string `json:"topic"`
}

var stopWords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"have": {}, "has": {}, "had": {}, "do": {}, "does": {}, "did": {}, "will": {}, "would": {}, "could": {},
	"should": {}, "may": {}, "might": {}, "must": {}, "shall": {}, "can": {}, "need": {}, "dare": {},
	"ought": {}, "used": {}, "to": {}, "of": {}, "in": {}, "for": {}, "on": {}, "with": {}, "at": {}, "by": {},
	"from": {}, "as": {}, "into": {}, "through": {}, "during": {}, "before": {}, "after": {}, "above": {},
	"below": {}, "between": {}, "out": {}, "off": {}, "over": {}, "under": {}, "again": {}, "further": {},
	"then": {}, "once": {}, "here": {}, "there": {}, "when": {}, "where": {}, "why": {}, "how": {}, "all": {},
	"each": {}, "few": {}, "more": {}, "most": {}, "other": {}, "some": {}, "such": {}, "no": {}, "nor": {},
	"not": {}, "only": {}, "own": {}, "same": {}, "so": {}, "than": {}, "too": {}, "very": {}, "just": {},
	"don": {}, "now": {}, "and": {}, "but": {}, "or": {}, "if": {}, "because": {}, "until": {}, "while": {},
	"about": {}, "against": {}, "what": {}, "which": {}, "who": {}, "whom": {}, "this": {}, "that": {},
	"these": {}, "those": {}, "am": {}, "i": {}, "me": {}, "my": {}, "myself": {}, "we": {}, "our": {}, "you": {},
	"your": {}, "he": {}, "him": {}, "his": {}, "she": {}, "her": {}, "it": {}, "its": {}, "they": {}, "them": {},
	"their": {}, "tell": {}, "said": {}, "say": {}, "know": {}, "think": {}, "like": {}, "get": {}, "got": {},
}

var memoryQueryPattern = regexp.MustCompile(`(?i)\b(remember|recall|what|who|when|where|my|name|tell me)\b`)

func extractSearchTerms(transcript string) []string {
	cleaned := regexp.MustCompile(`[^a-z0-9\s]`).ReplaceAllString(strings.ToLower(transcript), " ")
	words := strings.Fields(cleaned)
	seen := make(map[string]struct{})
	var terms []string
	for _, w := range words {
		if len(w) <= 2 {
			continue
		}
		if _, stop := stopWords[w]; stop {
			continue
		}
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		terms = append(terms, w)
		if len(terms) >= 12 {
			break
		}
	}
	return terms
}

func formatFactRows(rows []factRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		text := row.Fact
		if row.EntityName != nil && *row.EntityName != "" {
			text = *row.EntityName + ": " + text
		}
		out = append(out, text)
	}
	return out
}

func (k *Knowledge) GetUserProfileSummary(ctx context.Context, userID string) (string, error) {
	if !k.Enabled {
		return "", nil
	}

	var rows []struct {
		Summary *string `json:"summary"`
	}
	q := url.Values{}
	q.Set("select", "summary")
	q.Set("user_id", "eq."+userID)

	if err := k.DB.Get(ctx, "kb_user_profiles", q, &rows); err != nil {
		log.Warn("failed to load user profile", map[string]any{
			"userId": log.ShortID(userID),
			"error":  err.Error(),
		})
		return "", nil
	}
	if len(rows) == 0 || rows[0].Summary == nil {
		return "", nil
	}
	return strings.TrimSpace(*rows[0].Summary), nil
}

func (k *Knowledge) fetchRecentFacts(ctx context.Context, userID string, limit int) ([]factRow, error) {
	q := url.Values{}
	q.Set("select", "fact,entity_name,topic")
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []factRow
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		log.Warn("recent fact fetch failed", map[string]any{
			"userId": log.ShortID(userID),
			"error":  err.Error(),
		})
		return nil, err
	}
	return rows, nil
}

func (k *Knowledge) fetchFTSFacts(ctx context.Context, userID, tsQuery string, limit int) ([]factRow, error) {
	q := url.Values{}
	q.Set("select", "fact,entity_name,topic")
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")
	q.Set("search_vector", "fts.english.websearch."+tsQuery)
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []factRow
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (k *Knowledge) RetrieveFacts(ctx context.Context, userID, transcript string, limit int) ([]string, error) {
	if !k.Enabled {
		return nil, nil
	}

	terms := extractSearchTerms(transcript)
	isMemoryQuery := memoryQueryPattern.MatchString(transcript)

	var ftsRows []factRow
	if len(terms) > 0 {
		tsQuery := strings.Join(terms, " | ")
		rows, err := k.fetchFTSFacts(ctx, userID, tsQuery, limit*2)
		if err != nil {
			log.Warn("fact retrieval failed", map[string]any{
				"userId": log.ShortID(userID),
				"error":  err.Error(),
			})
		} else {
			ftsRows = rows
		}
	}

	if !isMemoryQuery && len(ftsRows) > 0 {
		formatted := formatFactRows(ftsRows)
		if len(formatted) > limit {
			formatted = formatted[:limit]
		}
		return formatted, nil
	}

	recentRows, _ := k.fetchRecentFacts(ctx, userID, limit*2)
	seen := make(map[string]struct{})
	merged := make([]factRow, 0, limit)

	for _, row := range append(ftsRows, recentRows...) {
		key := strings.ToLower(row.Fact)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, row)
		if len(merged) >= limit {
			break
		}
	}

	return formatFactRows(merged), nil
}
