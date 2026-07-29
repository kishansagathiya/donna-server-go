package voice

import (
	"context"
	"encoding/base64"
	"math"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/notes"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
	"github.com/kishansagathiya/donna/donna-server-go/internal/wav"
)

type audioChunkMeta struct {
	format     string
	sampleRate int
	channels   int
}

type Session struct {
	cfg           *config.Config
	engine        *pipeline.Engine
	notes         *notes.Sync
	conn          *websocket.Conn
	sessionID     string
	userID        string
	audioChunks   [][]byte
	audioMeta     *audioChunkMeta
	busy          bool
	started       bool
	chunkCount    int
	totalPCMBytes int
	mode          pipeline.InteractionMode
	clientNoteID  string
	ended         bool
	writeMu       sync.Mutex
}

func NewSession(conn *websocket.Conn, cfg *config.Config, engine *pipeline.Engine, noteSync *notes.Sync, userID string) *Session {
	s := &Session{
		cfg:       cfg,
		engine:    engine,
		notes:     noteSync,
		conn:      conn,
		sessionID: uuid.NewString(),
		userID:    userID,
	}
	if s.userID == "" {
		s.userID = uuid.NewString()
	}
	return s
}

func (s *Session) HandleMessage(ctx context.Context, raw string) error {
	msg, err := protocol.ParseClientMessage(raw)
	if err != nil {
		s.sendError("invalid_message", "Malformed JSON message")
		return nil
	}

	switch msg.Type {
	case "session.start":
		log.Print("← session.start", map[string]any{"session": log.ShortID(s.sessionID)})
		return s.handleSessionStart(ctx, msg)
	case "audio.chunk":
		s.handleAudioChunk(msg)
		return nil
	case "turn.end":
		log.Print("← turn.end", map[string]any{
			"session":  log.ShortID(s.sessionID),
			"chunks":   s.chunkCount,
			"pcmBytes": s.totalPCMBytes,
		})
		// Run STT off the read loop so outbound frames are not stalled.
		go s.handleTurnEnd(context.Background())
		return nil
	case "session.end":
		log.Print("← session.end", map[string]any{"session": log.ShortID(s.sessionID)})
		s.resetTurnBuffer()
		s.sendPhase(protocol.TurnPhaseIdle)
		s.End()
		return nil
	default:
		s.sendError("unknown_type", "Unknown message type")
		return nil
	}
}

func (s *Session) handleSessionStart(ctx context.Context, msg *protocol.ClientMessage) error {
	if s.started {
		return nil
	}
	s.started = true
	s.mode = pipeline.ModeTalk
	if msg.Mode != nil {
		s.mode = pipeline.ParseMode(*msg.Mode)
	}

	if msg.UserID != nil && *msg.UserID != "" {
		s.userID = *msg.UserID
	}
	if msg.SessionID != nil && *msg.SessionID != "" {
		s.sessionID = *msg.SessionID
	}
	s.clientNoteID = ""
	if msg.ClientNoteID != nil {
		if id := strings.TrimSpace(*msg.ClientNoteID); id != "" {
			if parsed, err := uuid.Parse(id); err == nil {
				s.clientNoteID = parsed.String()
			}
		}
	}

	if err := s.send(protocol.SessionReady(s.sessionID, s.userID)); err != nil {
		return err
	}
	s.sendPhase(protocol.TurnPhaseIdle)

	log.Print("→ session.ready", map[string]any{
		"session": log.ShortID(s.sessionID),
		"user":    log.ShortID(s.userID),
	})

	return nil
}

func (s *Session) End() {
	if s.ended {
		return
	}
	s.ended = true
	// Talk-mode chat persistence lives on POST /chat. Notes-mode voice uploads
	// do not create conversation rows here.
}

func (s *Session) handleAudioChunk(msg *protocol.ClientMessage) {
	if !s.started {
		log.Warn("audio.chunk rejected — session not started", map[string]any{
			"session": log.ShortID(s.sessionID),
			"seq":     derefInt(msg.Seq),
		})
		s.sendError("not_started", "Send session.start before audio.chunk")
		return
	}
	if s.busy {
		log.Warn("audio.chunk ignored — turn in progress", map[string]any{
			"session": log.ShortID(s.sessionID),
			"seq":     derefInt(msg.Seq),
		})
		s.sendPhase(protocol.TurnPhaseBusy)
		return
	}

	if s.audioMeta == nil {
		s.audioMeta = &audioChunkMeta{
			format:     derefString(msg.Format),
			sampleRate: derefInt(msg.SampleRate),
			channels:   derefInt(msg.Channels),
		}
	}

	if msg.Data == nil {
		s.sendError("invalid_message", "audio.chunk missing data")
		return
	}

	pcm, err := base64.StdEncoding.DecodeString(*msg.Data)
	if err != nil {
		s.sendError("invalid_message", "audio.chunk data is not valid base64")
		return
	}

	s.audioChunks = append(s.audioChunks, pcm)
	s.chunkCount++
	s.totalPCMBytes += len(pcm)

	if s.chunkCount == 1 {
		log.Print("← audio.chunk (first)", map[string]any{
			"session":    log.ShortID(s.sessionID),
			"seq":        derefInt(msg.Seq),
			"bytes":      len(pcm),
			"sampleRate": s.audioMeta.sampleRate,
			"channels":   s.audioMeta.channels,
		})
	} else if s.chunkCount%10 == 0 {
		seconds := estimatePCMSeconds(s.totalPCMBytes, s.audioMeta.sampleRate, s.audioMeta.channels)
		log.Print("← audio.chunk (buffering)", map[string]any{
			"session":       log.ShortID(s.sessionID),
			"chunks":        s.chunkCount,
			"pcmBytes":      s.totalPCMBytes,
			"approxSeconds": seconds,
			"lastSeq":       derefInt(msg.Seq),
		})
	}
}

func (s *Session) handleTurnEnd(ctx context.Context) error {
	if !s.started {
		s.sendError("not_started", "Send session.start before turn.end")
		return nil
	}
	if s.busy {
		s.sendPhase(protocol.TurnPhaseBusy)
		return nil
	}
	if s.audioMeta == nil || len(s.audioChunks) == 0 {
		log.Warn("turn.end with no audio buffered", map[string]any{
			"session": log.ShortID(s.sessionID),
			"chunks":  s.chunkCount,
		})
		s.sendError("empty_audio", "No audio buffered for this turn")
		return nil
	}

	pcm := concatChunks(s.audioChunks)
	audioMeta := *s.audioMeta
	quality := pipeline.AnalyzePCM16(pcm, audioMeta.sampleRate, audioMeta.channels)
	approxSeconds := estimatePCMSeconds(len(pcm), audioMeta.sampleRate, audioMeta.channels)
	committedChunks := s.chunkCount
	committedPCMBytes := len(pcm)
	s.resetTurnBuffer()
	s.busy = true

	defer func() { s.busy = false }()

	if !quality.ShouldProcess {
		log.Print("turn skipped — audio quality gate", map[string]any{
			"session":       log.ShortID(s.sessionID),
			"reason":        quality.Reason,
			"approxSeconds": approxSeconds,
			"avgRms":        quality.AvgRms,
			"peakRms":       quality.PeakRms,
		})
		skipped := true
		_ = s.send(protocol.TurnDone(protocol.EmptyTurnTimings(), skipped))
		s.sendPhase(protocol.TurnPhaseIdle)
		return nil
	}

	wavData := wav.PCM16ToWAV(pcm, wav.PCMFormat{
		SampleRate: audioMeta.sampleRate,
		Channels:   audioMeta.channels,
	})
	log.Print("turn commit — transcribing", map[string]any{
		"session":       log.ShortID(s.sessionID),
		"chunks":        committedChunks,
		"pcmBytes":      committedPCMBytes,
		"wavBytes":      len(wavData),
		"approxSeconds": approxSeconds,
		"mode":          string(s.mode),
	})

	result, err := s.engine.RunVoiceTurn(ctx, wavData, nil, pipeline.TurnCallbacks{
		OnPhase: func(phase protocol.TurnPhase) {
			s.sendPhase(phase)
		},
		OnTranscript: func(text string) {
			_ = s.send(protocol.TurnTranscript(text))
		},
	}, pipeline.TurnOptions{
		AudioMeta: quality.AudioQualityMeta,
		UserID:    s.userID,
		SessionID: s.sessionID,
		Mode:      s.mode,
	})
	if err != nil {
		log.Error("turn failed", map[string]any{
			"session": log.ShortID(s.sessionID),
			"error":   err.Error(),
		})
		s.sendPhase(protocol.TurnPhaseError)
		s.sendError("turn_failed", err.Error())
		s.sendPhase(protocol.TurnPhaseIdle)
		return nil
	}

	log.Print("turn complete", map[string]any{
		"session":    log.ShortID(s.sessionID),
		"transcript": result.Transcript,
		"skipped":    result.Skipped,
		"skipReason": result.SkipReason,
		"timings":    result.Timings,
	})

	noteID := ""
	if result.Transcript != "" && !result.Skipped && s.mode.IsNotes() {
		if s.notes != nil {
			content := result.Transcript
			audioBytes := wavData
			audio := &storage.NoteAudioInput{
				WAV:  audioBytes,
				Mime: "audio/wav",
			}
			if s.clientNoteID != "" {
				// Device capture uploads pass a stable clientNoteId. Create
				// synchronously before turn.done so retries stay idempotent
				// and the app can drop its local placeholder immediately.
				note, err := s.notes.CreateManualWithID(
					ctx,
					s.userID,
					s.clientNoteID,
					content,
					nil,
					audio,
				)
				if err != nil {
					log.Warn("failed to create note from voice", map[string]any{
						"session": log.ShortID(s.sessionID),
						"error":   err.Error(),
					})
					s.sendPhase(protocol.TurnPhaseError)
					s.sendError("note_create_failed", "Failed to save note")
					s.sendPhase(protocol.TurnPhaseIdle)
					return nil
				}
				noteID = note.ID
			} else {
				// Phone dictation keeps the previous fire-and-forget path so
				// turn.done is not blocked on note-audio upload latency.
				go func() {
					if _, err := s.notes.CreateManual(context.Background(), s.userID, content, nil, audio); err != nil {
						log.Warn("failed to create note from voice", map[string]any{
							"session": log.ShortID(s.sessionID),
							"error":   err.Error(),
						})
					}
				}()
			}
		}
	}

	// Talk mode: client receives turn.transcript and sends it through POST /chat.
	_ = s.send(protocol.TurnDoneWithNoteID(result.Timings, result.Skipped, noteID))
	s.sendPhase(protocol.TurnPhaseIdle)

	return nil
}

func (s *Session) resetTurnBuffer() {
	s.audioChunks = nil
	s.audioMeta = nil
	s.chunkCount = 0
	s.totalPCMBytes = 0
}

func (s *Session) sendPhase(phase protocol.TurnPhase) {
	if phase == protocol.TurnPhaseTranscribing ||
		phase == protocol.TurnPhaseGenerating ||
		phase == protocol.TurnPhaseSynthesizing ||
		phase == protocol.TurnPhaseError {
		log.Print("→ turn.phase "+string(phase), map[string]any{"session": log.ShortID(s.sessionID)})
	}
	_ = s.send(protocol.TurnPhaseMessage(phase))
}

func (s *Session) sendError(code, message string) {
	log.Warn("→ error", map[string]any{
		"session": log.ShortID(s.sessionID),
		"code":    code,
		"message": message,
	})
	_ = s.send(protocol.ErrorMessage(code, message))
}

func (s *Session) send(message protocol.ServerMessage) error {
	raw, err := protocol.SerializeServerMessage(message)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(context.Background(), websocket.MessageText, []byte(raw))
}

func estimatePCMSeconds(pcmBytes, sampleRate, channels int) float64 {
	bytesPerSecond := sampleRate * channels * 2
	return math.Round(float64(pcmBytes)/float64(bytesPerSecond)*10) / 10
}

func concatChunks(chunks [][]byte) []byte {
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

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
