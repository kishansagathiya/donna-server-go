package voicelive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/memory"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Session struct {
	clientConn *websocket.Conn
	cfg        *config.Config
	retriever  *memory.Retriever
	convs      *storage.Conversations
	userID     string

	sessionID      string
	conversationID string
	gemini         *geminiClient

	mu              sync.Mutex
	started         bool
	ended           bool
	setupDone       bool
	turnIndex       int
	userTranscript  strings.Builder
	asstTranscript  strings.Builder
	clientWriteMu   sync.Mutex
}

func NewSession(
	clientConn *websocket.Conn,
	cfg *config.Config,
	retriever *memory.Retriever,
	convs *storage.Conversations,
	userID string,
) *Session {
	return &Session{
		clientConn: clientConn,
		cfg:        cfg,
		retriever:  retriever,
		convs:      convs,
		userID:     userID,
		sessionID:  uuid.NewString(),
	}
}

func (s *Session) HandleMessage(ctx context.Context, raw []byte) error {
	msg, err := parseClientMessage(raw)
	if err != nil {
		return s.sendError("invalid_message", "Invalid message")
	}

	switch msg.Type {
	case ClientSessionStart:
		return s.start(ctx)
	case ClientAudioChunk:
		return s.forwardAudio(ctx, msg.Data)
	case ClientSessionEnd:
		s.End(ctx)
		return nil
	default:
		return s.sendError("unknown_type", "Unknown message type")
	}
}

func (s *Session) start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return s.sendError("already_started", "Session already started")
	}
	if strings.TrimSpace(s.cfg.GeminiAPIKey) == "" {
		s.mu.Unlock()
		return s.sendError("live_unavailable", "Realtime Voice is not configured (missing GEMINI_API_KEY)")
	}
	s.started = true
	s.mu.Unlock()

	systemPrompt := s.buildSystemPrompt(ctx)

	gemini, err := dialGemini(ctx, s.cfg.GeminiAPIKey)
	if err != nil {
		log.Warn("voicelive gemini dial failed", map[string]any{"error": err.Error()})
		return s.sendError("gemini_connect_failed", "Could not connect to realtime voice")
	}
	s.gemini = gemini

	if err := gemini.setup(ctx, s.cfg.LiveModel, systemPrompt, s.cfg.LiveVoiceName); err != nil {
		gemini.close()
		s.gemini = nil
		log.Warn("voicelive gemini setup failed", map[string]any{"error": err.Error()})
		return s.sendError("gemini_setup_failed", "Could not start realtime voice session")
	}

	if s.convs != nil && s.convs.Enabled && s.userID != "" {
		if id, err := s.convs.Create(ctx, s.userID, s.sessionID); err != nil {
			log.Warn("voicelive conversation create failed", map[string]any{"error": err.Error()})
		} else {
			s.conversationID = id
		}
	}

	go s.readGeminiLoop(context.Background())
	return nil
}

func (s *Session) buildSystemPrompt(ctx context.Context) string {
	base := strings.TrimSpace(s.cfg.SystemPrompt)
	if base == "" {
		base = "You are Donna, a sharp and thoughtful second-brain companion."
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nYou are in a live voice conversation. Speak naturally, keep replies concise unless the user wants depth, and wait when the user is clearly still thinking. Use retrieve_memory when prior personal context would help.")

	seed := s.seedMemory(ctx)
	if seed != "" {
		b.WriteString("\n\nKnown context about this user (may be incomplete; call retrieve_memory for more):\n")
		b.WriteString(seed)
	}
	return b.String()
}

func (s *Session) seedMemory(ctx context.Context) string {
	if s.retriever == nil || s.userID == "" {
		return ""
	}
	seedCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	result := s.retriever.Retrieve(seedCtx, s.userID, s.sessionID, "important facts preferences people projects", s.cfg.MemoryMinScore)
	return formatMemoryHits(result.Hits, 1800)
}

func (s *Session) retrieveMemory(ctx context.Context, query string) map[string]any {
	query = strings.TrimSpace(query)
	if query == "" {
		return map[string]any{"memories": "", "count": 0}
	}
	if s.retriever == nil || s.userID == "" {
		return map[string]any{"memories": "", "count": 0, "note": "memory unavailable"}
	}
	result := s.retriever.Retrieve(ctx, s.userID, s.sessionID, query, s.cfg.MemoryMinScore)
	text := formatMemoryHits(result.Hits, 2400)
	return map[string]any{
		"memories": text,
		"count":    len(result.Hits),
	}
}

func formatMemoryHits(hits []memory.Hit, maxChars int) string {
	if len(hits) == 0 {
		return ""
	}
	var parts []string
	n := 0
	for _, h := range hits {
		t := strings.TrimSpace(h.Text)
		if t == "" {
			continue
		}
		if maxChars > 0 && n+len(t) > maxChars {
			break
		}
		parts = append(parts, t)
		n += len(t) + 3
	}
	return strings.Join(parts, " | ")
}

func (s *Session) forwardAudio(ctx context.Context, data string) error {
	s.mu.Lock()
	started := s.started
	setupDone := s.setupDone
	ended := s.ended
	gemini := s.gemini
	s.mu.Unlock()

	if ended || !started {
		return nil
	}
	if gemini == nil || !setupDone {
		return nil
	}
	if strings.TrimSpace(data) == "" {
		return nil
	}
	if err := gemini.sendAudio(ctx, data); err != nil {
		log.Warn("voicelive forward audio failed", map[string]any{"error": err.Error()})
		return s.sendError("audio_forward_failed", "Audio stream interrupted")
	}
	return nil
}

func (s *Session) readGeminiLoop(ctx context.Context) {
	for {
		s.mu.Lock()
		ended := s.ended
		gemini := s.gemini
		s.mu.Unlock()
		if ended || gemini == nil {
			return
		}

		msg, err := gemini.read(ctx)
		if err != nil {
			s.mu.Lock()
			ended = s.ended
			s.mu.Unlock()
			if !ended {
				log.Warn("voicelive gemini read ended", map[string]any{"error": err.Error()})
				_ = s.sendError("gemini_disconnected", "Realtime voice disconnected")
			}
			s.End(ctx)
			return
		}

		if msg.SetupComplete != nil {
			s.mu.Lock()
			already := s.setupDone
			s.setupDone = true
			s.mu.Unlock()
			if !already {
				_ = s.send(ServerMessage{
					Type:      ServerSessionReady,
					SessionID: s.sessionID,
				})
			}
			continue
		}

		if msg.ToolCall != nil {
			for _, call := range msg.ToolCall.FunctionCalls {
				s.handleToolCall(ctx, call)
			}
			continue
		}

		if msg.ServerContent == nil {
			continue
		}
		sc := msg.ServerContent

		if sc.Interrupted {
			_ = s.send(ServerMessage{Type: ServerInterrupted})
		}

		if sc.InputTranscription != nil {
			text := strings.TrimSpace(sc.InputTranscription.Text)
			if text != "" {
				s.mu.Lock()
				s.userTranscript.WriteString(text)
				if !strings.HasSuffix(text, " ") {
					s.userTranscript.WriteByte(' ')
				}
				s.mu.Unlock()
				_ = s.send(ServerMessage{
					Type:  ServerTranscript,
					Role:  "user",
					Text:  text,
					Final: false,
				})
			}
		}

		if sc.OutputTranscription != nil {
			text := strings.TrimSpace(sc.OutputTranscription.Text)
			if text != "" {
				s.mu.Lock()
				s.asstTranscript.WriteString(text)
				if !strings.HasSuffix(text, " ") {
					s.asstTranscript.WriteByte(' ')
				}
				s.mu.Unlock()
				_ = s.send(ServerMessage{
					Type:  ServerTranscript,
					Role:  "assistant",
					Text:  text,
					Final: false,
				})
			}
		}

		if sc.ModelTurn != nil {
			for _, part := range sc.ModelTurn.Parts {
				if part.InlineData == nil || part.InlineData.Data == "" {
					continue
				}
				_ = s.send(ServerMessage{
					Type:       ServerAudioChunk,
					Data:       part.InlineData.Data,
					SampleRate: 24_000,
					Channels:   1,
				})
			}
		}

		if sc.TurnComplete {
			s.flushTurn(ctx)
		}
	}
}

func (s *Session) handleToolCall(ctx context.Context, call geminiFnCall) {
	if s.gemini == nil {
		return
	}
	name := strings.TrimSpace(call.Name)
	id := strings.TrimSpace(call.ID)
	var response map[string]any
	switch name {
	case "retrieve_memory":
		query, _ := call.Args["query"].(string)
		response = s.retrieveMemory(ctx, query)
	default:
		response = map[string]any{"error": fmt.Sprintf("unknown tool: %s", name)}
	}
	if err := s.gemini.sendToolResponse(ctx, id, name, response); err != nil {
		log.Warn("voicelive tool response failed", map[string]any{
			"error": err.Error(),
			"tool":  name,
		})
	}
}

func (s *Session) flushTurn(ctx context.Context) {
	s.mu.Lock()
	user := strings.TrimSpace(s.userTranscript.String())
	asst := strings.TrimSpace(s.asstTranscript.String())
	s.userTranscript.Reset()
	s.asstTranscript.Reset()
	convID := s.conversationID
	turnIdx := s.turnIndex
	if user != "" || asst != "" {
		s.turnIndex++
	}
	s.mu.Unlock()

	// Mark streamed captions final without re-sending full text (avoids duplicate bubbles).
	if user != "" {
		_ = s.send(ServerMessage{Type: ServerTranscript, Role: "user", Final: true})
	}
	if asst != "" {
		_ = s.send(ServerMessage{Type: ServerTranscript, Role: "assistant", Final: true})
	}

	if convID == "" || s.convs == nil || !s.convs.Enabled || s.userID == "" {
		return
	}
	if user == "" && asst == "" {
		return
	}
	s.convs.PersistTurnAsync(storage.SaveTurnInput{
		ConversationID:      convID,
		UserID:              s.userID,
		TurnIndex:           turnIdx,
		UserTranscript:      user,
		AssistantTranscript: asst,
		Timings:             protocol.TurnTimings{},
	})
}

func (s *Session) End(ctx context.Context) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	gemini := s.gemini
	s.gemini = nil
	convID := s.conversationID
	s.mu.Unlock()

	s.flushTurn(ctx)
	if gemini != nil {
		gemini.close()
	}
	if convID != "" && s.convs != nil {
		s.convs.EndAsync(convID)
	}
	_ = s.send(ServerMessage{Type: ServerSessionEnded})
}

func (s *Session) sendError(code, message string) error {
	log.Print("voicelive error", map[string]any{
		"code":      code,
		"message":   message,
		"sessionId": s.sessionID,
		"userId":    log.ShortID(s.userID),
	})
	return s.send(ServerMessage{Type: ServerError, Message: message})
}

func (s *Session) send(msg ServerMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.clientWriteMu.Lock()
	defer s.clientWriteMu.Unlock()
	return s.clientConn.Write(context.Background(), websocket.MessageText, data)
}
