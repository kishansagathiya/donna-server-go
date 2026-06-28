package pipeline

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
	"github.com/kishansagathiya/donna/donna-server-go/internal/wav"
)

const retryPrompt = "Sorry, I missed that — what were you saying?"

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
}

type TurnCallbacks struct {
	OnPhase      func(protocol.TurnPhase)
	OnTranscript func(string)
	OnReply      func(string)
	OnAudioChunk func(seq int, chunk providers.AudioChunk)
}

type TurnOptions struct {
	AudioMeta AudioQualityMeta
	CanRetry  bool
	UserID    string
	SessionID string
	Mode      InteractionMode
}

type Engine struct {
	Config      *config.Config
	STT         *providers.STT
	LLM         *providers.LLM
	TTS         *providers.TTS
	KB          *storage.Knowledge
	Notes       *storage.Notes
	Preferences *storage.Preferences
}

func (e *Engine) RunVoiceTurn(
	ctx context.Context,
	wavData []byte,
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
	transcript, sttMs, err := e.STT.TranscribeWAV(ctx, wavData)
	if err != nil {
		return TurnResult{}, err
	}
	timings.STTMs = sttMs

	classification := ClassifyTranscript(transcript, options.AudioMeta)
	if classification == TranscriptNoise {
		return finishSkipped(phase, timings, t0, true, "noise"), nil
	}

	if classification == TranscriptFailedAttempt {
		if options.Mode.IsNotes() || !options.CanRetry {
			return finishSkipped(phase, timings, t0, true, "failed_attempt"), nil
		}
		phase(protocol.TurnPhaseSynthesizing)
		_, _ = e.streamTTSToClient(ctx, retryPrompt, callbacks, &timings, nil, func() {
			if callbacks.OnReply != nil {
				callbacks.OnReply(retryPrompt)
			}
		})
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

	if options.Mode.IsNotes() {
		timings.TotalMs = int(time.Since(t0).Milliseconds())
		phase(protocol.TurnPhaseDone)
		return TurnResult{
			Transcript: transcript,
			Timings:    timings,
		}, nil
	}

	phase(protocol.TurnPhaseGenerating)
	augStart := time.Now()
	augmented, profileSummary := e.loadTurnContext(ctx, transcript, options.UserID, options.SessionID)
	timings.AugmentMs = int(time.Since(augStart).Milliseconds())

	systemPrompt := e.Config.SystemPrompt
	if profileSummary != "" {
		systemPrompt = systemPrompt + "\n\nKnown about this user:\n" + profileSummary
	}

	messages := providers.BuildLLMMessages(systemPrompt, history, augmented.Text)

	llmStart := time.Now()
	replyText := ""
	firstToken := true

	err = e.llmForUser(ctx, options.UserID).StreamCompletion(ctx, messages, func(chunk string) error {
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

	replyText = strings.TrimSpace(replyText)

	var assistantAudio *AssistantAudio
	if replyText != "" {
		phase(protocol.TurnPhaseSynthesizing)
		audio, err := e.streamTTSToClient(ctx, replyText, callbacks, &timings, nil, nil)
		if err != nil {
			return TurnResult{}, err
		}
		if audio != nil {
			saveFormat := audio.Format
			data := audio.Data
			audioSampleRate := audio.SampleRate
			audioChannels := audio.Channels
			if audio.Format == "pcm16" {
				if audioSampleRate == 0 {
					audioSampleRate = 24000
				}
				if audioChannels == 0 {
					audioChannels = 1
				}
				data = wav.PCM16ToWAV(data, wav.PCMFormat{
					SampleRate: audioSampleRate,
					Channels:   audioChannels,
				})
				saveFormat = "wav"
			}
			assistantAudio = &AssistantAudio{
				Format: saveFormat,
				Data:   data,
			}
		}
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

func (e *Engine) llmForUser(ctx context.Context, userID string) *providers.LLM {
	if e.Preferences == nil || userID == "" {
		return e.LLM
	}
	model, err := e.Preferences.GetLLMModel(ctx, userID)
	if err != nil || model == "" {
		return e.LLM
	}
	for _, allowed := range e.Config.LLMModels {
		if model == allowed {
			return e.LLM.WithModel(model)
		}
	}
	return e.LLM
}

func (e *Engine) loadTurnContext(ctx context.Context, transcript, userID, sessionID string) (TranscriptAugmentation, string) {
	var (
		augmented      TranscriptAugmentation
		profileSummary string
		wg             sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		augmented = DefaultAugment(ctx, e.KB, e.Notes, transcript, userID, sessionID)
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

func (e *Engine) streamTTSToClient(
	ctx context.Context,
	text string,
	callbacks TurnCallbacks,
	timings *protocol.TurnTimings,
	ttsFirstByteRecorded *bool,
	onFirstByte func(),
) (*AssistantAudio, error) {
	ttsStart := time.Now()
	firstByte := true
	seq := 0
	var parts [][]byte
	var format string
	var sampleRate, channels int

	err := e.TTS.SynthesizeSpeech(ctx, text, func(chunk providers.AudioChunk) error {
		if firstByte {
			if ttsFirstByteRecorded == nil || !*ttsFirstByteRecorded {
				timings.TTSFirstByteMs = int(time.Since(ttsStart).Milliseconds())
				if ttsFirstByteRecorded != nil {
					*ttsFirstByteRecorded = true
				}
			}
			if onFirstByte != nil {
				onFirstByte()
			}
			firstByte = false
		}
		format = chunk.Format
		if chunk.SampleRate > 0 {
			sampleRate = chunk.SampleRate
		}
		if chunk.Channels > 0 {
			channels = chunk.Channels
		}
		part := make([]byte, len(chunk.Data))
		copy(part, chunk.Data)
		parts = append(parts, part)
		if callbacks.OnAudioChunk != nil {
			callbacks.OnAudioChunk(seq, chunk)
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
		Format:     format,
		Data:       concatBytes(parts),
		SampleRate: sampleRate,
		Channels:   channels,
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
