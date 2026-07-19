package pipeline

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type TextTurnCallbacks struct {
	OnPhase  func(protocol.TurnPhase)
	OnStatus func(phase protocol.TurnPhase, host string)
	OnReply  func(string)
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
		if callbacks.OnStatus != nil {
			callbacks.OnStatus(p, "")
			return
		}
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

	turnTools := e.Tools
	if e.ConnectorTools != nil && options.UserID != "" {
		if merged := e.ConnectorTools(ctx, options.UserID, e.Tools); merged != nil {
			turnTools = merged
		}
	}
	toolsEnabled := e.Config != nil && e.Config.ChatToolsEnabled && turnTools != nil && turnTools.Len() > 0
	if toolsEnabled {
		systemPrompt = systemPrompt + "\n\n" + tools.BrowseToolsPrompt
		if e.ConnectorPrompt != "" && registryHasPrefix(turnTools, "granola_") {
			systemPrompt = systemPrompt + "\n\n" + e.ConnectorPrompt
		}
	}

	messages := providers.BuildLLMMessages(systemPrompt, history, augmented.Text)

	llm, route := e.resolveLLMWithPreference(options.UserID, message, prefs.LLMModel)
	timings.PreLLMMs = int(time.Since(preLLMStart).Milliseconds())
	llmStart := time.Now()
	replyText := ""
	firstToken := true

	baseOpts := providers.ChatCompletionOptions{
		WebSearch:           options.WebSearch,
		WebSearchMaxResults: 3,
	}

	var toolCitations []tools.Citation
	if toolsEnabled {
		loopResult, err := tools.RunToolLoop(ctx, llm, messages, turnTools, baseOpts, tools.LoopLimits{}, tools.LoopCallbacks{
			OnPhase:  phase,
			OnStatus: callbacks.OnStatus,
			OnReply: func(text string) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if firstToken {
					timings.LLMFirstTokenMs = int(time.Since(llmStart).Milliseconds())
					firstToken = false
				}
				replyText = text
				if callbacks.OnReply != nil {
					callbacks.OnReply(replyText)
				}
				return nil
			},
		})
		if err != nil {
			return TurnResult{}, err
		}
		replyText = loopResult.ReplyText
		toolCitations = loopResult.Citations
		if firstToken && loopResult.FirstToken > 0 {
			timings.LLMFirstTokenMs = int(loopResult.FirstToken.Milliseconds())
			firstToken = false
		}
	} else {
		meta, err := llm.StreamCompletionWithOptions(ctx, messages, baseOpts, func(chunk string) error {
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
		toolCitations = webToToolCitations(meta.WebCitations)
	}

	timings.TotalMs = int(time.Since(t0).Milliseconds())
	phase(protocol.TurnPhaseDone)

	citations := append(augmented.Citations, toolCitationsToMemory(toolCitations)...)

	return TurnResult{
		Transcript: message,
		ReplyText:  replyText,
		Timings:    timings,
		Citations:  citations,
		Route:      route,
	}, nil
}

func webToToolCitations(citations []providers.WebCitation) []tools.Citation {
	out := make([]tools.Citation, 0, len(citations))
	for _, citation := range citations {
		out = append(out, tools.Citation{
			URL:     citation.URL,
			Title:   citation.Title,
			Content: citation.Content,
		})
	}
	return out
}

func toolCitationsToMemory(citations []tools.Citation) []MemoryCitation {
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
		source := strings.TrimSpace(citation.Source)
		if source == "" {
			source = "web"
		}
		out = append(out, MemoryCitation{
			Source: source,
			Text:   truncateCitationText(text),
			URL:    strings.TrimSpace(citation.URL),
			Title:  strings.TrimSpace(citation.Title),
		})
	}
	return out
}

// webCitations kept for tests that call it directly.
func webCitations(citations []providers.WebCitation) []MemoryCitation {
	return toolCitationsToMemory(webToToolCitations(citations))
}

func registryHasPrefix(reg *tools.Registry, prefix string) bool {
	if reg == nil || prefix == "" {
		return false
	}
	for _, def := range reg.Definitions() {
		if strings.HasPrefix(def.Function.Name, prefix) {
			return true
		}
	}
	return false
}
