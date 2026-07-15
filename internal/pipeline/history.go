package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

const historySummaryPrompt = `Summarize the earlier part of this conversation for continuity.
Preserve key facts, decisions, preferences, open questions, and names.
Keep it under 180 words. Use compact prose or short bullets. Do not invent details.`

// PackHistory keeps the most recent max messages verbatim. When older turns
// would be dropped, it summarizes them and prepends a system summary message.
// On summarization failure it falls back to plain truncation.
func PackHistory(
	ctx context.Context,
	llm *providers.LLM,
	history []providers.ChatMessage,
	max int,
) []providers.ChatMessage {
	if max <= 0 || len(history) <= max {
		return history
	}

	older := history[:len(history)-max]
	recent := append([]providers.ChatMessage(nil), history[len(history)-max:]...)

	if llm == nil {
		return recent
	}

	summary, err := summarizeHistory(ctx, llm, older)
	if err != nil || strings.TrimSpace(summary) == "" {
		log.Warn("history summarization failed; truncating", map[string]any{
			"error":      errString(err),
			"olderCount": len(older),
			"keptCount":  len(recent),
		})
		return recent
	}

	summaryMsg := providers.ChatMessage{
		Role:    "system",
		Content: "Earlier conversation summary:\n" + strings.TrimSpace(summary),
	}
	return append([]providers.ChatMessage{summaryMsg}, recent...)
}

func summarizeHistory(ctx context.Context, llm *providers.LLM, older []providers.ChatMessage) (string, error) {
	var b strings.Builder
	for _, msg := range older {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if role == "" || content == "" {
			continue
		}
		// Keep the transcript compact for the summarizer.
		if len(content) > 500 {
			content = content[:500] + "…"
		}
		fmt.Fprintf(&b, "%s: %s\n", role, content)
	}
	transcript := strings.TrimSpace(b.String())
	if transcript == "" {
		return "", nil
	}

	messages := []providers.ChatMessage{
		{Role: "system", Content: historySummaryPrompt},
		{Role: "user", Content: transcript},
	}
	return llm.CompleteOnce(ctx, messages)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
