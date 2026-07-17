package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/notes"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Engine        *pipeline.Engine
	Conversations *storage.Conversations
	Notes         *notes.Sync
}

type chatRequest struct {
	Message     string                  `json:"message"`
	History     []providers.ChatMessage `json:"history,omitempty"`
	SessionID   string                  `json:"session_id,omitempty"`
	Mode        string                  `json:"mode,omitempty"`
	Attachments []ChatAttachment        `json:"attachments,omitempty"`
	WebSearch   bool                    `json:"web_search,omitempty"`
}

type chatResponse struct {
	Reply               string                    `json:"reply"`
	SessionID           string                    `json:"session_id"`
	Timings             protocol.TurnTimings      `json:"timings"`
	Citations           []pipeline.MemoryCitation `json:"citations,omitempty"`
	Route               *pipeline.RouteDecision   `json:"route,omitempty"`
	GroundedUserMessage string                    `json:"grounded_user_message,omitempty"`
	AttachmentLabels    []string                  `json:"attachment_labels,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":   "unauthorized",
			"message": "Valid Supabase JWT required",
		})
		return
	}

	var body chatRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "invalid_body",
			"message": "Expected JSON body with message field",
		})
		return
	}

	grounded, err := groundChatTurn(body.Message, body.Attachments)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "invalid_attachments",
			"message": err.Error(),
		})
		return
	}

	sessionID := strings.TrimSpace(body.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	history := append([]providers.ChatMessage(nil), body.History...)
	persistHistory := history
	history = h.packHistory(r.Context(), history)

	mode := pipeline.ParseMode(body.Mode)

	if wantsStream(r) {
		h.streamReply(w, r, userID, sessionID, grounded, history, persistHistory, mode, body.WebSearch)
		return
	}

	result, err := h.Engine.RunTextTurn(r.Context(), grounded.GroundedMessage, history, pipeline.TextTurnCallbacks{}, pipeline.TurnOptions{
		UserID:    userID,
		SessionID: sessionID,
		Mode:      mode,
		WebSearch: body.WebSearch,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "chat_failed",
			"message": err.Error(),
		})
		return
	}

	if result.Skipped {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "skipped",
			"message": result.SkipReason,
		})
		return
	}

	h.persistAfterTurn(r.Context(), userID, sessionID, grounded.GroundedMessage, result.ReplyText, persistHistory, result.Timings, result.Skipped, mode)

	reply := result.ReplyText
	if mode.IsNotes() {
		reply = ""
	}
	writeJSON(w, http.StatusOK, buildChatResponse(reply, sessionID, result, grounded))
}

func (h *Handler) streamReply(
	w http.ResponseWriter,
	r *http.Request,
	userID, sessionID string,
	grounded groundedTurn,
	history []providers.ChatMessage,
	persistHistory []providers.ChatMessage,
	mode pipeline.InteractionMode,
	webSearch bool,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "stream_unsupported",
			"message": "Streaming is not supported by this server",
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeSSE := func(event, data string) {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	writeSSE("session", mustJSON(map[string]string{"session_id": sessionID}))

	result, err := h.Engine.RunTextTurn(r.Context(), grounded.GroundedMessage, history, pipeline.TextTurnCallbacks{
		OnPhase: func(phase protocol.TurnPhase) {
			writeSSE("phase", mustJSON(string(phase)))
		},
		OnReply: func(text string) {
			writeSSE("chunk", mustJSON(map[string]string{"text": text}))
		},
	}, pipeline.TurnOptions{
		UserID:    userID,
		SessionID: sessionID,
		Mode:      mode,
		WebSearch: webSearch,
	})
	if err != nil {
		writeSSE("error", mustJSON(map[string]string{"message": err.Error()}))
		return
	}

	if result.Skipped {
		writeSSE("error", mustJSON(map[string]string{"message": result.SkipReason}))
		return
	}

	h.persistAfterTurn(r.Context(), userID, sessionID, grounded.GroundedMessage, result.ReplyText, persistHistory, result.Timings, result.Skipped, mode)

	reply := result.ReplyText
	if mode.IsNotes() {
		reply = ""
	}

	if len(result.Citations) > 0 {
		writeSSE("citations", mustJSON(map[string]any{"citations": result.Citations}))
	}

	writeSSE("done", mustJSON(buildChatResponse(reply, sessionID, result, grounded)))
}

func buildChatResponse(reply, sessionID string, result pipeline.TurnResult, grounded groundedTurn) chatResponse {
	resp := chatResponse{
		Reply:               reply,
		SessionID:           sessionID,
		Timings:             result.Timings,
		Citations:           result.Citations,
		GroundedUserMessage: grounded.GroundedMessage,
		AttachmentLabels:    grounded.Labels,
	}
	if result.Route.Model != "" {
		route := result.Route
		resp.Route = &route
	}
	return resp
}

// packHistory caps prompt history. When older turns would be dropped, it
// summarizes them (falling back to truncation on failure). Budget is
// Config.MaxHistoryMessages (DONNA_MAX_HISTORY_MESSAGES).
func (h *Handler) packHistory(ctx context.Context, history []providers.ChatMessage) []providers.ChatMessage {
	if h.Engine == nil || h.Engine.Config == nil {
		return history
	}
	max := h.Engine.Config.MaxHistoryMessages
	if max <= 0 || len(history) <= max {
		return history
	}

	summarizer := h.Engine.LLM
	if h.Engine.Config.LLMFastModel != "" {
		summarizer = h.Engine.LLM.WithModel(h.Engine.Config.LLMFastModel)
	}
	return pipeline.PackHistory(ctx, summarizer, history, max)
}

func (h *Handler) persistAfterTurn(
	ctx context.Context,
	userID, sessionID, userMessage, assistantMessage string,
	history []providers.ChatMessage,
	timings protocol.TurnTimings,
	skipped bool,
	mode pipeline.InteractionMode,
) {
	if skipped {
		return
	}
	if mode.IsNotes() {
		h.persistNote(ctx, userID, userMessage)
		return
	}
	h.persistFacts(userID, sessionID, userMessage)
	h.persistTurn(userID, sessionID, userMessage, assistantMessage, history, timings, skipped)
}

func (h *Handler) persistNote(ctx context.Context, userID, content string) {
	if h.Notes == nil || strings.TrimSpace(content) == "" {
		return
	}
	go func() {
		bg := context.Background()
		if _, err := h.Notes.CreateManual(bg, userID, strings.TrimSpace(content), nil, nil); err != nil {
			log.Warn("failed to create note from chat", map[string]any{
				"userId": log.ShortID(userID),
				"error":  err.Error(),
			})
		}
	}()
}

func (h *Handler) persistTurn(
	userID, sessionID, userMessage, assistantMessage string,
	history []providers.ChatMessage,
	timings protocol.TurnTimings,
	skipped bool,
) {
	if skipped || h.Conversations == nil || !h.Conversations.Enabled {
		return
	}

	turnIndex := textTurnIndex(history)
	go func() {
		ctx := context.Background()
		conversationID, err := h.Conversations.GetOrCreateText(ctx, userID, sessionID)
		if err != nil {
			log.Warn("failed to get or create text conversation", map[string]any{
				"userId":    log.ShortID(userID),
				"sessionId": sessionID,
				"error":     err.Error(),
			})
			return
		}
		h.Conversations.PersistTurnAsync(storage.SaveTurnInput{
			ConversationID:      conversationID,
			UserID:              userID,
			TurnIndex:           turnIndex,
			UserTranscript:      userMessage,
			AssistantTranscript: assistantMessage,
			Timings:             timings,
		})
	}()
}

func textTurnIndex(history []providers.ChatMessage) int {
	n := 0
	for _, msg := range history {
		if msg.Role == "user" {
			n++
		}
	}
	return n
}

func (h *Handler) persistFacts(userID, sessionID, transcript string) {
	if h.Engine.KB == nil || transcript == "" {
		return
	}
	knowledge.PersistLiveFactsAsync(h.Engine.KB, knowledge.LiveFactsInput{
		UserID:         userID,
		Transcript:     transcript,
		ConversationID: sessionID,
		TurnIndex:      0,
	})
}

func wantsStream(r *http.Request) bool {
	if r.URL.Query().Get("stream") == "1" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/event-stream")
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
