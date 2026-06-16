package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

type Handler struct {
	Engine *pipeline.Engine
}

type chatRequest struct {
	Message   string                  `json:"message"`
	History   []providers.ChatMessage `json:"history,omitempty"`
	SessionID string                  `json:"session_id,omitempty"`
}

type chatResponse struct {
	Reply     string              `json:"reply"`
	SessionID string              `json:"session_id"`
	Timings   protocol.TurnTimings `json:"timings"`
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

	message := strings.TrimSpace(body.Message)
	if message == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "empty_message",
			"message": "Message cannot be empty",
		})
		return
	}

	sessionID := strings.TrimSpace(body.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	history := append([]providers.ChatMessage(nil), body.History...)

	if wantsStream(r) {
		h.streamReply(w, r, userID, sessionID, message, history)
		return
	}

	result, err := h.Engine.RunTextTurn(r.Context(), message, history, pipeline.TextTurnCallbacks{}, pipeline.TurnOptions{
		UserID:    userID,
		SessionID: sessionID,
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

	h.persistFacts(userID, sessionID, result.Transcript)

	writeJSON(w, http.StatusOK, chatResponse{
		Reply:     result.ReplyText,
		SessionID: sessionID,
		Timings:   result.Timings,
	})
}

func (h *Handler) streamReply(
	w http.ResponseWriter,
	r *http.Request,
	userID, sessionID, message string,
	history []providers.ChatMessage,
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

	writeSSE := func(event, data string) {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	writeSSE("session", mustJSON(map[string]string{"session_id": sessionID}))

	result, err := h.Engine.RunTextTurn(r.Context(), message, history, pipeline.TextTurnCallbacks{
		OnPhase: func(phase protocol.TurnPhase) {
			writeSSE("phase", string(phase))
		},
		OnReply: func(text string) {
			writeSSE("chunk", mustJSON(map[string]string{"text": text}))
		},
	}, pipeline.TurnOptions{
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		writeSSE("error", mustJSON(map[string]string{"message": err.Error()}))
		return
	}

	if result.Skipped {
		writeSSE("error", mustJSON(map[string]string{"message": result.SkipReason}))
		return
	}

	h.persistFacts(userID, sessionID, result.Transcript)

	writeSSE("done", mustJSON(chatResponse{
		Reply:     result.ReplyText,
		SessionID: sessionID,
		Timings:   result.Timings,
	}))
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
