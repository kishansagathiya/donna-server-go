package pipeline

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type TextTurnCallbacks struct {
	OnPhase    func(protocol.TurnPhase)
	OnReply    func(string)
	OnActivity func() // SSE/proxy keepalive while the model is thinking
}

func (e *Engine) RunTextTurn(
	ctx context.Context,
	userMessage string,
	history []providers.ChatMessage,
	callbacks TextTurnCallbacks,
	options TurnOptions,
) (TurnResult, error) {
	t0 := time.Now()
	timings := protocol.EmptyTurnTimings()

	phase := func(p protocol.TurnPhase) {
		if callbacks.OnPhase != nil {
			callbacks.OnPhase(p)
		}
	}

	message := strings.TrimSpace(userMessage)
	if message == "" {
		return finishSkipped(phase, timings, t0, true, "empty"), nil
	}

	if options.Mode.IsNotes() {
		timings.TotalMs = int(time.Since(t0).Milliseconds())
		phase(protocol.TurnPhaseDone)
		return TurnResult{
			Transcript: message,
			Timings:    timings,
		}, nil
	}

	phase(protocol.TurnPhaseGenerating)
	preLLMStart := time.Now()
	var (
		augmented      TranscriptAugmentation
		profileSummary string
		prefs          storage.PrefsRow
		augmentMs      int
		preferencesMs  int
		wg             sync.WaitGroup
	)

	// Context retrieval and user preferences are independent. Start them
	// together so a cold preferences cache does not add another database wait
	// after memory/profile augmentation has completed.
	wg.Add(2)
	go func() {
		defer wg.Done()
		start := time.Now()
		augmented, profileSummary = e.loadTurnContext(ctx, message, options.UserID, options.SessionID)
		augmentMs = int(time.Since(start).Milliseconds())
	}()
	go func() {
		defer wg.Done()
		start := time.Now()
		if e.Preferences != nil && options.UserID != "" {
			prefs, _ = e.Preferences.GetChatPreferences(ctx, options.UserID)
		}
		preferencesMs = int(time.Since(start).Milliseconds())
	}()
	wg.Wait()
	timings.AugmentMs = augmentMs
	timings.PreferencesMs = preferencesMs

	systemPrompt := e.resolveSystemPromptWithPreferences(prefs)
	if profileSummary != "" {
		systemPrompt = systemPrompt + "\n\nKnown about this user:\n" + profileSummary
	}

	messages := providers.BuildLLMMessages(systemPrompt, history, augmented.Text)

	llm, route := e.resolveLLMWithPreference(options.UserID, message, prefs.LLMModel)
	timings.PreLLMMs = int(time.Since(preLLMStart).Milliseconds())
	llmStart := time.Now()
	replyText := ""
	firstToken := true

	// Reasoning models (kimi-k3) + :online can sit quiet for a long time.
	// Exclude reasoning from the stream and keep max_tokens high enough that
	// thinking does not consume the entire completion budget.
	maxTokens := 8192
	if strings.Contains(strings.ToLower(route.Model), "kimi-k3") ||
		strings.HasSuffix(route.Model, ":online") ||
		options.WebSearch {
		maxTokens = 16384
	}

	meta, err := llm.StreamCompletionWithOptions(ctx, messages, providers.ChatCompletionOptions{
		WebSearch:           options.WebSearch,
		WebSearchMaxResults: 3,
		MaxTokens:           maxTokens,
		ExcludeReasoning:    true,
		OnActivity:          callbacks.OnActivity,
	}, func(chunk string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if firstToken {
			timings.LLMFirstTokenMs = int(time.Since(llmStart).Milliseconds())
			firstToken = false
		}
		replyText += chunk
		if callbacks.OnReply != nil {
			callbacks.OnReply(replyText)
		}
		return nil
	})
	if err != nil {
		return TurnResult{}, err
	}

	timings.TotalMs = int(time.Since(t0).Milliseconds())
	phase(protocol.TurnPhaseDone)

	return TurnResult{
		Transcript: message,
		ReplyText:  replyText,
		Timings:    timings,
		Citations:  append(augmented.Citations, webCitations(meta.WebCitations)...),
		Route:      route,
	}, nil
}

func webCitations(citations []providers.WebCitation) []MemoryCitation {
	out := make([]MemoryCitation, 0, len(citations))
	for _, citation := range citations {
		text := strings.TrimSpace(citation.Title)
		if text == "" {
			text = strings.TrimSpace(citation.Content)
		}
		if text == "" {
			text = strings.TrimSpace(citation.URL)
		}
		if text == "" {
			continue
		}
		out = append(out, MemoryCitation{
			Source: "web",
			Text:   truncateCitationText(text),
			URL:    strings.TrimSpace(citation.URL),
			Title:  strings.TrimSpace(citation.Title),
		})
	}
	return out
}
