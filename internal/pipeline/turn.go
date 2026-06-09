package pipeline

import (
	"context"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const retryPrompt = "Sorry, I missed that — what were you saying?"

type AssistantAudio struct {
	Format string
	Data   []byte
}

type TurnResult struct {
	Transcript     string
	ReplyText      string
	Timings        protocol.TurnTimings
	Skipped        bool
	SkipReason     string
	UsedRetry      bool
	AssistantAudio *AssistantAudio
}

type TurnCallbacks struct {
	OnPhase      func(protocol.TurnPhase)
	OnTranscript func(string)
	OnReply      func(string)
	OnAudioChunk func(seq int, format string, data []byte)
}

type TurnOptions struct {
	AudioMeta AudioQualityMeta
	CanRetry  bool
	UserID    string
	SessionID string
}

type Engine struct {
	Config *config.Config
	STT    *providers.STT
	LLM    *providers.LLM
	TTS    *providers.TTS
	KB     *storage.Knowledge
}

func (e *Engine) RunVoiceTurn(
	ctx context.Context,
	wav []byte,
	history []providers.ChatMessage,
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
	transcript, sttMs, err := e.STT.TranscribeWAV(ctx, wav)
	if err != nil {
		return TurnResult{}, err
	}
	timings.STTMs = sttMs

	classification := ClassifyTranscript(transcript, options.AudioMeta)
	if classification == TranscriptNoise {
		return finishSkipped(phase, timings, t0, true, "noise"), nil
	}

	if classification == TranscriptFailedAttempt {
		if !options.CanRetry {
			return finishSkipped(phase, timings, t0, true, "failed_attempt"), nil
		}
		phase(protocol.TurnPhaseSynthesizing)
		if callbacks.OnReply != nil {
			callbacks.OnReply(retryPrompt)
		}
		_, _ = e.streamTTSToClient(ctx, retryPrompt, callbacks, &timings)
		timings.TotalMs = int(time.Since(t0).Milliseconds())
		phase(protocol.TurnPhaseDone)
		return TurnResult{
			ReplyText: retryPrompt,
			Timings:   timings,
			UsedRetry: true,
		}, nil
	}

	if callbacks.OnTranscript != nil {
		callbacks.OnTranscript(transcript)
	}

	phase(protocol.TurnPhaseGenerating)
	augStart := time.Now()
	augmented := DefaultAugment(ctx, e.KB, transcript, options.UserID, options.SessionID)

	profileSummary := ""
	if e.KB != nil && e.KB.Enabled {
		profileSummary, _ = e.KB.GetUserProfileSummary(ctx, options.UserID)
	}
	timings.AugmentMs = int(time.Since(augStart).Milliseconds())

	systemPrompt := e.Config.SystemPrompt
	if profileSummary != "" {
		systemPrompt = systemPrompt + "\n\nKnown about this user:\n" + profileSummary
	}

	messages := providers.BuildLLMMessages(systemPrompt, history, augmented.Text)

	llmStart := time.Now()
	replyText := ""
	firstToken := true
	err = e.LLM.StreamCompletion(ctx, messages, func(chunk string) error {
		if firstToken {
			timings.LLMFirstTokenMs = int(time.Since(llmStart).Milliseconds())
			firstToken = false
		}
		replyText += chunk
		return nil
	})
	if err != nil {
		return TurnResult{}, err
	}

	if callbacks.OnReply != nil {
		callbacks.OnReply(replyText)
	}

	phase(protocol.TurnPhaseSynthesizing)
	assistantAudio, err := e.streamTTSToClient(ctx, replyText, callbacks, &timings)
	if err != nil {
		return TurnResult{}, err
	}

	timings.TotalMs = int(time.Since(t0).Milliseconds())
	phase(protocol.TurnPhaseDone)

	return TurnResult{
		Transcript:     transcript,
		ReplyText:      replyText,
		Timings:        timings,
		AssistantAudio: assistantAudio,
	}, nil
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

func (e *Engine) streamTTSToClient(
	ctx context.Context,
	text string,
	callbacks TurnCallbacks,
	timings *protocol.TurnTimings,
) (*AssistantAudio, error) {
	ttsStart := time.Now()
	firstByte := true
	seq := 0
	var parts [][]byte
	var format string

	err := e.TTS.SynthesizeSpeech(ctx, text, func(chunk providers.AudioChunk) error {
		if firstByte {
			timings.TTSFirstByteMs = int(time.Since(ttsStart).Milliseconds())
			firstByte = false
		}
		format = chunk.Format
		part := make([]byte, len(chunk.Data))
		copy(part, chunk.Data)
		parts = append(parts, part)
		if callbacks.OnAudioChunk != nil {
			callbacks.OnAudioChunk(seq, chunk.Format, part)
		}
		seq++
		return nil
	})
	if err != nil {
		return nil, err
	}

	if format == "" || len(parts) == 0 {
		return nil, nil
	}

	return &AssistantAudio{
		Format: format,
		Data:   concatBytes(parts),
	}, nil
}

func concatBytes(chunks [][]byte) []byte {
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	out := make([]byte, total)
	offset := 0
	for _, c := range chunks {
		copy(out[offset:], c)
		offset += len(c)
	}
	return out
}
