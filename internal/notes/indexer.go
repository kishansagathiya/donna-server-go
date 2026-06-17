package notes

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

var (
	urgentPattern    = regexp.MustCompile(`(?i)\b(urgent|asap|today|now|immediately|deadline)\b`)
	importantPattern = regexp.MustCompile(`(?i)\b(important|must|critical|key|priority)\b`)
)

type IndexingResult struct {
	IsUrgent    bool
	IsImportant bool
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
		} else {
			log.Warn("note LLM indexing failed", map[string]any{
				"noteId": log.ShortID(noteID),
				"error":  err.Error(),
			})
		}
	}

	return idx.Store.ApplyIndexerFlags(ctx, noteID, result.IsUrgent, result.IsImportant)
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
	}
}

func (idx *Indexer) indexWithLLM(ctx context.Context, noteText string) (IndexingResult, error) {
	systemPrompt := strings.Join([]string{
		"You label notes for a personal notes app.",
		"Return only strict JSON with keys:",
		"- isUrgent: boolean",
		"- isImportant: boolean",
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
		IsUrgent    *bool `json:"isUrgent"`
		IsImportant *bool `json:"isImportant"`
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
	return out, nil
}
