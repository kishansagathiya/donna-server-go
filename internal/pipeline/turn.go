package pipeline

import (
	"context"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type AssistantAudio struct {
	Format     string
	Data       []byte
	SampleRate int
	Channels   int
}

type TurnResult struct {
	Transcript     string
	ReplyText      string
	Timings        protocol.TurnTimings
	Skipped        bool
	SkipReason     string
	UsedRetry      bool
	AssistantAudio *AssistantAudio
	Citations      []MemoryCitation
	Route          RouteDecision
}

type TurnCallbacks struct {
	OnPhase      func(protocol.TurnPhase)
	OnTranscript func(string)
}

type TurnOptions struct {
	AudioMeta AudioQualityMeta
	CanRetry  bool
	UserID    string
	SessionID string
	Mode      InteractionMode
	WebSearch bool
}

// ConnectorToolsFunc merges per-user connector tools into the base registry.
// Returning nil keeps the base registry unchanged. Failures must degrade
// gracefully (return base or nil) so chat continues without connectors.
type ConnectorToolsFunc func(ctx context.Context, userID string, base *tools.Registry) *tools.Registry

type Engine struct {
	Config      *config.Config
	STT         *providers.STT
	LLM         *providers.LLM
	KB          *storage.Knowledge
	Notes       *storage.Notes
	Preferences *storage.Preferences
	Flags       *featureflags.Resolver
	Tools       *tools.Registry
	// ConnectorTools optionally adds per-user live connector tools for text chat.
	ConnectorTools  ConnectorToolsFunc
	ConnectorPrompt string
}

// RunVoiceTurn transcribes buffered voice audio. Talk-mode chat replies go
// through RunTextTurn / POST /chat — voice is speech-to-text only. Notes mode
// returns the transcript for note creation without an LLM reply.
func (e *Engine) RunVoiceTurn(
	ctx context.Context,
	wavData []byte,
	_ []providers.ChatMessage,
	callbacks TurnCallbacks,
	options TurnOptions,
) (TurnResult, error) {
	t0 := time.Now()
	timings := protocol.EmptyTurnTimings()

	phase := func(p protocol.TurnPhase) {
		if callbacks.OnPhase != nil {
			callbacks.OnPhase(p)
		}
	}

	phase(protocol.TurnPhaseTranscribing)
	transcript, sttMs, err := e.STT.TranscribeWAV(ctx, wavData)
	if err != nil {
		return TurnResult{}, err
	}
	timings.STTMs = sttMs

	classification := ClassifyTranscript(transcript, options.AudioMeta)
	if classification == TranscriptNoise || classification == TranscriptFailedAttempt {
		reason := "noise"
		if classification == TranscriptFailedAttempt {
			reason = "failed_attempt"
		}
		return finishSkipped(phase, timings, t0, true, reason), nil
	}

	if callbacks.OnTranscript != nil {
		callbacks.OnTranscript(transcript)
	}

	timings.TotalMs = int(time.Since(t0).Milliseconds())
	phase(protocol.TurnPhaseDone)
	return TurnResult{
		Transcript: transcript,
		Timings:    timings,
	}, nil
}

// llmForUser resolves the model for a user without message-based auto-routing.
// Prefer resolveLLM on turn paths so fast/strong routing can apply.
func (e *Engine) llmForUser(ctx context.Context, userID string) *providers.LLM {
	llm, _ := e.resolveLLM(ctx, userID, "")
	return llm
}

func (e *Engine) loadTurnContext(ctx context.Context, transcript, userID, sessionID string) (TranscriptAugmentation, string) {
	base := TranscriptAugmentation{Transcript: transcript}
	if !NeedsUserContext(transcript) {
		base.Text = FormatAugmentedUserMessage(base)
		return base, ""
	}

	minScore := defaultMemoryMinScore
	if e.Config != nil && e.Config.MemoryMinScore > 0 {
		minScore = e.Config.MemoryMinScore
	}

	useMemoryV2 := false
	if e.Flags != nil && userID != "" {
		if flags, err := e.Flags.NotesMemoryV2ForUser(ctx, userID); err == nil {
			useMemoryV2 = flags.MemoryRetrieval
		}
	}

	var (
		augmented      TranscriptAugmentation
		profileSummary string
		wg             sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		augmented = DefaultAugmentOpts(ctx, e.KB, e.Notes, transcript, userID, sessionID, minScore, useMemoryV2)
	}()
	go func() {
		defer wg.Done()
		if e.KB != nil && e.KB.Enabled {
			profileSummary, _ = e.KB.GetUserProfileSummary(ctx, userID)
		}
	}()
	wg.Wait()
	return augmented, profileSummary
}

func finishSkipped(phase func(protocol.TurnPhase), timings protocol.TurnTimings, t0 time.Time, skipped bool, reason string) TurnResult {
	timings.TotalMs = int(time.Since(t0).Milliseconds())
	phase(protocol.TurnPhaseDone)
	return TurnResult{
		Timings:    timings,
		Skipped:    skipped,
		SkipReason: reason,
	}
}
