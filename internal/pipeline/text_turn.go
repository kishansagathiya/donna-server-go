package pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

type TextTurnCallbacks struct {
	OnPhase func(protocol.TurnPhase)
	OnReply func(string)
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
	augStart := time.Now()
	// Retrieve memory and the user profile in parallel (the voice path already
	// does this via loadTurnContext); running them sequentially here roughly
	// doubled pre-LLM latency on the text chat hot path.
	augmented, profileSummary := e.loadTurnContext(ctx, message, options.UserID, options.SessionID)
	timings.AugmentMs = int(time.Since(augStart).Milliseconds())

	systemPrompt := e.resolveSystemPrompt(ctx, options.UserID)
	if profileSummary != "" {
		systemPrompt = systemPrompt + "\n\nKnown about this user:\n" + profileSummary
	}

	messages := providers.BuildLLMMessages(systemPrompt, history, augmented.Text)

	llm, route := e.resolveLLM(ctx, options.UserID, message)
	llmStart := time.Now()
	replyText := ""
	firstToken := true

	err := llm.StreamCompletion(ctx, messages, func(chunk string) error {
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
		Citations:  augmented.Citations,
		Route:      route,
	}, nil
}
