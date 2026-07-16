package intents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Matcher proposes action runs for newly extracted intents.
type Matcher interface {
	MatchIntent(ctx context.Context, userID string, intent storage.Intent) error
}

type Extractor struct {
	Store   *storage.ActionsStore
	LLM     *providers.LLM
	Matcher Matcher
}

type SourceInput struct {
	UserID          string
	SourceType      string // note | conversation_turn
	SourceID        string
	SourceTurnIndex *int
	Label           string
	Content         string
}

func (e *Extractor) ExtractFromSource(ctx context.Context, in SourceInput) error {
	if e == nil || e.Store == nil || !e.Store.Enabled {
		return nil
	}
	content := strings.TrimSpace(in.Content)
	if content == "" || in.UserID == "" {
		return nil
	}
	if in.SourceType != "note" && in.SourceType != "conversation_turn" {
		return fmt.Errorf("invalid_source_type")
	}

	extracted, err := e.extract(ctx, in.SourceType, in.Label, content)
	if err != nil {
		return err
	}
	if len(extracted) == 0 {
		return nil
	}

	sourceID := in.SourceID
	var sourceIDPtr *string
	if sourceID != "" {
		sourceIDPtr = &sourceID
	}

	for _, item := range extracted {
		kind := normalizeKind(item.Kind)
		if kind == "" || kind == "create_note" {
			continue
		}
		summary := strings.TrimSpace(item.Summary)
		if summary == "" {
			continue
		}
		slotsJSON, err := json.Marshal(normalizeSlots(item.Slots))
		if err != nil {
			slotsJSON = []byte(`{}`)
		}
		intent, created, err := e.Store.UpsertOpenIntent(ctx, in.UserID, storage.NewIntentInput{
			Kind:            kind,
			Summary:         summary,
			Slots:           slotsJSON,
			SourceType:      in.SourceType,
			SourceID:        sourceIDPtr,
			SourceTurnIndex: in.SourceTurnIndex,
			Confidence:      item.Confidence,
		})
		if err != nil {
			log.Warn("intent upsert failed", map[string]any{
				"user":   log.ShortID(in.UserID),
				"kind":   kind,
				"source": in.SourceType,
				"error":  err.Error(),
			})
			continue
		}
		if !created {
			continue
		}
		if e.Matcher != nil {
			if err := e.Matcher.MatchIntent(ctx, in.UserID, intent); err != nil {
				log.Warn("intent match failed", map[string]any{
					"user":     log.ShortID(in.UserID),
					"intentId": log.ShortID(intent.ID),
					"error":    err.Error(),
				})
			}
		}
	}
	return nil
}

func (e *Extractor) extract(ctx context.Context, sourceType, label, content string) ([]ExtractedIntent, error) {
	if e.LLM == nil {
		return heuristicExtract(content), nil
	}

	messages := []providers.ChatMessage{
		{Role: "system", Content: ExtractorSystemPrompt},
		{Role: "user", Content: BuildExtractorUserMessage(sourceType, label, content)},
	}
	raw, err := e.LLM.CompleteOnce(ctx, messages)
	if err != nil {
		log.Warn("intent extractor LLM failed; using heuristics", map[string]any{
			"error": err.Error(),
		})
		return heuristicExtract(content), nil
	}
	out := parseExtractorOutput(raw)
	if len(out.Intents) == 0 {
		return heuristicExtract(content), nil
	}
	return out.Intents, nil
}

func parseExtractorOutput(raw string) extractorOutput {
	trimmed := strings.TrimSpace(raw)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return extractorOutput{}
	}
	var out extractorOutput
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &out); err != nil {
		return extractorOutput{}
	}
	return out
}

func normalizeKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	k = strings.ReplaceAll(k, " ", "_")
	k = strings.ReplaceAll(k, "-", "_")
	return k
}

func normalizeSlots(slots map[string]string) map[string]string {
	if slots == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(slots))
	for k, v := range slots {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	return out
}

func heuristicExtract(content string) []ExtractedIntent {
	lower := strings.ToLower(content)
	var out []ExtractedIntent

	switch {
	case strings.Contains(lower, "remind") || strings.Contains(lower, "reminder"):
		out = append(out, ExtractedIntent{
			Kind:    "remind",
			Summary: truncate(content, 160),
			Slots:   map[string]string{"title": truncate(content, 80)},
		})
	case strings.Contains(lower, "call ") || strings.Contains(lower, "follow up") || strings.Contains(lower, "follow-up") || strings.Contains(lower, "text "):
		out = append(out, ExtractedIntent{
			Kind:    "follow_up",
			Summary: truncate(content, 160),
			Slots:   map[string]string{"body": truncate(content, 400)},
		})
	case strings.Contains(lower, "http://") || strings.Contains(lower, "https://"):
		url := firstURL(content)
		if url != "" {
			out = append(out, ExtractedIntent{
				Kind:    "open_url",
				Summary: "Open " + url,
				Slots:   map[string]string{"url": url},
			})
		}
	case strings.Contains(lower, "schedule") || strings.Contains(lower, "meeting") || strings.Contains(lower, "appointment"):
		out = append(out, ExtractedIntent{
			Kind:    "schedule",
			Summary: truncate(content, 160),
			Slots:   map[string]string{"title": truncate(content, 80)},
		})
	case strings.Contains(lower, "message ") || strings.Contains(lower, "email ") || strings.Contains(lower, "draft "):
		out = append(out, ExtractedIntent{
			Kind:    "draft_message",
			Summary: truncate(content, 160),
			Slots:   map[string]string{"body": truncate(content, 400)},
		})
	}
	return out
}

func firstURL(content string) string {
	for _, prefix := range []string{"https://", "http://"} {
		idx := strings.Index(strings.ToLower(content), prefix)
		if idx < 0 {
			continue
		}
		rest := content[idx:]
		end := len(rest)
		for i, r := range rest {
			if r == ' ' || r == '\n' || r == '\t' || r == ')' || r == ']' || r == '"' || r == '\'' {
				end = i
				break
			}
		}
		return strings.TrimRight(rest[:end], ".,;:")
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-1]) + "…"
}
