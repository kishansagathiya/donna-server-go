package notes

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

var (
	urgentPattern    = regexp.MustCompile(`(?i)\b(urgent|asap|today|now|immediately|deadline)\b`)
	importantPattern = regexp.MustCompile(`(?i)\b(important|must|critical|key|priority)\b`)
)

// heuristic category buckets, mirrored from Steve's regex tagger.
var categoryPatterns = []struct {
	category string
	pattern  *regexp.Regexp
}{
	{"health", regexp.MustCompile(`(?i)\b(workout|exercise|gym|run|run|sleep|diet|doctor|medicine|health|weight|fitness)\b`)},
	{"work", regexp.MustCompile(`(?i)\b(work|project|deadline|meeting|client|email|report|presentation|stakeholder)\b`)},
	{"finance", regexp.MustCompile(`(?i)\b(budget|invoice|payment|tax|salary|invest|money|bank|expense|refund)\b`)},
	{"learning", regexp.MustCompile(`(?i)\b(learn|study|course|book|read|tutorial|research|lecture|class)\b`)},
	{"personal", regexp.MustCompile(`(?i)\b(family|friend|birthday|anniversary|home|trip|travel|vacation|groceries)\b`)},
	{"ideas", regexp.MustCompile(`(?i)\b(idea|concept|hypothesis|brainstorm|sketch|prototype|vision)\b`)},
}

var keywordStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
	"to": true, "of": true, "in": true, "on": true, "for": true, "with": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
	"this": true, "that": true, "it": true, "as": true, "at": true, "by": true,
	"from": true, "have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "could": true, "should": true,
	"my": true, "your": true, "his": true, "her": true, "their": true, "our": true,
	"i": true, "you": true, "he": true, "she": true, "they": true, "we": true,
	"me": true, "him": true, "them": true, "us": true,
	"if": true, "then": true, "so": true, "just": true, "about": true,
	"into": true, "out": true, "up": true, "down": true, "over": true, "under": true,
}

type IndexingResult struct {
	IsUrgent    bool
	IsImportant bool
	Keywords    []string
	Category    string
}

type Indexer struct {
	Store *storage.Notes
	LLM   *providers.LLM
}

func (idx *Indexer) IndexNote(ctx context.Context, noteID string) error {
	if idx.Store == nil || !idx.Store.Enabled {
		return nil
	}

	content, err := idx.loadNoteContent(ctx, noteID)
	if err != nil {
		return err
	}
	if content == "" {
		return nil
	}

	heuristic := heuristicIndex(content)
	result := heuristic

	if idx.LLM != nil && idx.LLM.APIKey != "" {
		if llmResult, err := idx.indexWithLLM(ctx, content); err == nil {
			if llmResult.IsUrgent {
				result.IsUrgent = true
			}
			if llmResult.IsImportant {
				result.IsImportant = true
			}
			if len(llmResult.Keywords) > 0 {
				result.Keywords = mergeKeywords(result.Keywords, llmResult.Keywords)
			}
			if llmResult.Category != "" {
				result.Category = llmResult.Category
			}
		} else {
			log.Warn("note LLM indexing failed", map[string]any{
				"noteId": log.ShortID(noteID),
				"error":  err.Error(),
			})
		}
	}

	if len(result.Keywords) == 0 {
		result.Keywords = heuristicKeywords(content)
	}
	result.Keywords = normalizeKeywords(result.Keywords)

	return idx.Store.ApplyIndexerMeta(ctx, noteID, result.IsUrgent, result.IsImportant, result.Keywords, result.Category)
}

func (idx *Indexer) loadNoteContent(ctx context.Context, noteID string) (string, error) {
	q := url.Values{}
	q.Set("select", "content")
	q.Set("id", "eq."+noteID)

	var rows []struct {
		Content string `json:"content"`
	}
	if err := idx.Store.DB.Get(ctx, "notes", q, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].Content, nil
}

func heuristicIndex(text string) IndexingResult {
	return IndexingResult{
		IsUrgent:    urgentPattern.MatchString(text),
		IsImportant: importantPattern.MatchString(text),
		Category:    heuristicCategory(text),
	}
}

func heuristicCategory(text string) string {
	for _, bucket := range categoryPatterns {
		if bucket.pattern.MatchString(text) {
			return bucket.category
		}
	}
	return ""
}

// heuristicKeywords extracts frequent non-stopword tokens (capped at 8).
func heuristicKeywords(text string) []string {
	words := regexp.MustCompile(`[A-Za-z][A-Za-z0-9'-]*`).FindAllString(text, -1)
	counts := make(map[string]int)
	for _, w := range words {
		lower := strings.ToLower(w)
		if len(lower) < 3 || keywordStopwords[lower] {
			continue
		}
		counts[lower]++
	}
	type kv struct {
		word  string
		count int
	}
	var ranked []kv
	for w, c := range counts {
		ranked = append(ranked, kv{w, c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].word < ranked[j].word
	})
	limit := 8
	if len(ranked) < limit {
		limit = len(ranked)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, ranked[i].word)
	}
	return out
}

// mergeKeywords de-duplicates and unions two keyword lists (capped at 10).
func mergeKeywords(a, b []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, w := range append(append([]string{}, a...), b...) {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func normalizeKeywords(keywords []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(keywords))
	for _, w := range keywords {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func (idx *Indexer) indexWithLLM(ctx context.Context, noteText string) (IndexingResult, error) {
	systemPrompt := strings.Join([]string{
		"You label notes for a personal notes app.",
		"Return only strict JSON with keys:",
		"- isUrgent: boolean",
		"- isImportant: boolean",
		"- keywords: array of strings (3-8 lowercase keywords describing the note)",
		"- category: string (one of: health, work, finance, learning, personal, ideas; empty if none fit)",
	}, "\n")

	messages := []providers.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: noteText},
	}

	raw, err := idx.LLM.CompleteOnce(ctx, messages)
	if err != nil {
		return IndexingResult{}, err
	}

	trimmed := strings.TrimSpace(raw)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 {
		return IndexingResult{}, nil
	}

	var parsed struct {
		IsUrgent    *bool    `json:"isUrgent"`
		IsImportant *bool    `json:"isImportant"`
		Keywords    []string `json:"keywords"`
		Category    string   `json:"category"`
	}
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &parsed); err != nil {
		return IndexingResult{}, err
	}

	out := IndexingResult{}
	if parsed.IsUrgent != nil {
		out.IsUrgent = *parsed.IsUrgent
	}
	if parsed.IsImportant != nil {
		out.IsImportant = *parsed.IsImportant
	}
	out.Keywords = parsed.Keywords
	out.Category = strings.ToLower(strings.TrimSpace(parsed.Category))
	return out, nil
}
