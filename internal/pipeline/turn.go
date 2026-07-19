package pipeline

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
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
	Citations      []MemoryCitation
	Route          RouteDecision
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
	WebSearch bool
}

type Engine struct {
	Config      *config.Config
	STT         *providers.STT
	LLM         *providers.LLM
	TTS         *providers.TTS
	KB          *storage.Knowledge
	Notes       *storage.Notes
	Preferences *storage.Preferences
	Tools       *tools.Registry
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

	systemPrompt := e.resolveSystemPrompt(ctx, options.UserID)
	if profileSummary != "" {
		systemPrompt = systemPrompt + "\n\nKnown about this user:\n" + profileSummary
	}

	messages := providers.BuildLLMMessages(systemPrompt, history, augmented.Text)

	llm, route := e.resolveLLM(ctx, options.UserID, transcript)
	llmStart := time.Now()
	replyText := ""
	firstToken := true
	ttsStarted := false
	ttsFirstByteRecorded := false
	var audioParts [][]byte
	var audioFormat string
	var audioSampleRate, audioChannels int
	sentenceBuf := &sentenceBuffer{}
	var audioMu sync.Mutex
	var ttsErr error
	sentenceQueue := make(chan string, 16)
	ttsDone := make(chan struct{})

	speakSentence := func(sentence string) error {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			return nil
		}
		if !ttsStarted {
			phase(protocol.TurnPhaseSynthesizing)
			ttsStarted = true
		}

		audio, err := e.streamTTSToClient(
			ctx,
			sentence,
			callbacks,
			&timings,
			&ttsFirstByteRecorded,
			nil,
		)
		if err != nil {
			return err
		}
		if audio != nil {
			audioMu.Lock()
			audioFormat = audio.Format
			audioParts = append(audioParts, audio.Data)
			if audio.SampleRate > 0 {
				audioSampleRate = audio.SampleRate
			}
			if audio.Channels > 0 {
				audioChannels = audio.Channels
			}
			audioMu.Unlock()
		}
		return nil
	}

	go func() {
		defer close(ttsDone)
		for sentence := range sentenceQueue {
			if err := speakSentence(sentence); err != nil {
				ttsErr = err
				return
			}
		}
	}()

	enqueueSentence := func(sentence string) error {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sentenceQueue <- sentence:
			return nil
		}
	}

	err = llm.StreamCompletion(ctx, messages, func(chunk string) error {
		if firstToken {
			timings.LLMFirstTokenMs = int(time.Since(llmStart).Milliseconds())
			firstToken = false
		}
		replyText += chunk
		if callbacks.OnReply != nil {
			callbacks.OnReply(replyText)
		}
		for _, sentence := range sentenceBuf.add(chunk) {
			if err := enqueueSentence(sentence); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		close(sentenceQueue)
		<-ttsDone
		return TurnResult{}, err
	}

	if err := enqueueSentence(sentenceBuf.flush()); err != nil {
		close(sentenceQueue)
		<-ttsDone
		return TurnResult{}, err
	}
	close(sentenceQueue)
	<-ttsDone
	if ttsErr != nil {
		return TurnResult{}, ttsErr
	}

	replyText = strings.TrimSpace(replyText)

	var assistantAudio *AssistantAudio
	audioMu.Lock()
	defer audioMu.Unlock()
	if audioFormat != "" && len(audioParts) > 0 {
		data := concatBytes(audioParts)
		saveFormat := audioFormat
		if audioFormat == "pcm16" {
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

	timings.TotalMs = int(time.Since(t0).Milliseconds())
	phase(protocol.TurnPhaseDone)

	return TurnResult{
		Transcript:     transcript,
		ReplyText:      replyText,
		Timings:        timings,
		AssistantAudio: assistantAudio,
		Citations:      augmented.Citations,
		Route:          route,
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

	var (
		augmented      TranscriptAugmentation
		profileSummary string
		wg             sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		augmented = DefaultAugment(ctx, e.KB, e.Notes, transcript, userID, sessionID, minScore)
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

	err := e.TTS.SynthesizeSpeech(ctx, providers.PrepareTextForSpeech(text), func(chunk providers.AudioChunk) error {
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
