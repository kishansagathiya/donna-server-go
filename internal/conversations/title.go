package conversations

import (
	"context"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

// LLMTitleGenerator creates short conversation titles via OpenRouter.
type LLMTitleGenerator struct {
	LLM *providers.LLM
}

func (g *LLMTitleGenerator) GenerateConversationTitle(ctx context.Context, userText, assistantText string) (string, error) {
	if g == nil || g.LLM == nil {
		return "", fmt.Errorf("llm unavailable")
	}

	userText = strings.TrimSpace(userText)
	assistantText = strings.TrimSpace(assistantText)
	if userText == "" && assistantText == "" {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("Write a concise chat title (3-6 words) for this conversation.\n")
	b.WriteString("Rules: no quotes, no trailing punctuation, no emojis, Title Case preferred.\n")
	if userText != "" {
		b.WriteString("User: ")
		b.WriteString(truncateForTitlePrompt(userText, 400))
		b.WriteByte('\n')
	}
	if assistantText != "" {
		b.WriteString("Assistant: ")
		b.WriteString(truncateForTitlePrompt(assistantText, 400))
		b.WriteByte('\n')
	}
	b.WriteString("Title:")

	messages := []providers.ChatMessage{
		{Role: "user", Content: b.String()},
	}
	title, err := g.LLM.CompleteOnce(ctx, messages)
	if err != nil {
		return "", err
	}
	title = strings.TrimSpace(title)
	title = strings.Trim(title, `"'`)
	// Keep a single line.
	if i := strings.IndexAny(title, "\n\r"); i >= 0 {
		title = strings.TrimSpace(title[:i])
	}
	return title, nil
}

func truncateForTitlePrompt(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max-3] + "..."
}
