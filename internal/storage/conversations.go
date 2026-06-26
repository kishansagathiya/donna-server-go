package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

const audioBucket = "conversation-audio"

type Conversations struct {
	DB      *Supabase
	Enabled bool
}

type SaveTurnInput struct {
	ConversationID       string
	UserID             string
	TurnIndex          int
	UserTranscript     string
	AssistantTranscript string
	UserWav            []byte
	AssistantAudio     []byte
	AssistantFormat    string
	Timings            protocol.TurnTimings
}

func (c *Conversations) Create(ctx context.Context, userID, voiceSessionID string) (string, error) {
	var rows []struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"user_id":          userID,
		"voice_session_id": voiceSessionID,
		"channel":          "voice",
	}
	if err := c.DB.Insert(ctx, "conversations", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to create conversation")
	}

	log.Print("conversation created", map[string]any{
		"conversationId": rows[0].ID,
		"voiceSessionId": voiceSessionID,
		"userId":         log.ShortID(userID),
	})
	return rows[0].ID, nil
}

func (c *Conversations) GetOrCreateText(ctx context.Context, userID, clientSessionID string) (string, error) {
	if !c.Enabled {
		return "", fmt.Errorf("conversation persistence disabled")
	}
	clientSessionID = strings.TrimSpace(clientSessionID)
	if clientSessionID == "" {
		return "", fmt.Errorf("missing client session id")
	}

	q := url.Values{}
	q.Set("select", "id")
	q.Set("user_id", "eq."+userID)
	q.Set("channel", "eq.text")
	q.Set("client_session_id", "eq."+clientSessionID)
	q.Set("ended_at", "is.null")
	q.Set("limit", "1")

	var existing []struct {
		ID string `json:"id"`
	}
	if err := c.DB.Get(ctx, "conversations", q, &existing); err != nil {
		return "", err
	}
	if len(existing) > 0 {
		return existing[0].ID, nil
	}

	var rows []struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"user_id":            userID,
		"channel":            "text",
		"client_session_id":  clientSessionID,
	}
	if err := c.DB.Insert(ctx, "conversations", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to create text conversation")
	}

	log.Print("text conversation created", map[string]any{
		"conversationId":  rows[0].ID,
		"clientSessionId": clientSessionID,
		"userId":          log.ShortID(userID),
	})
	return rows[0].ID, nil
}

func (c *Conversations) End(ctx context.Context, conversationID string) error {
	q := url.Values{}
	q.Set("id", "eq."+conversationID)
	q.Set("ended_at", "is.null")

	body := map[string]string{
		"ended_at": time.Now().UTC().Format(time.RFC3339),
	}
	if err := c.DB.Patch(ctx, "conversations", q, body); err != nil {
		return err
	}
	log.Print("conversation ended", map[string]any{"conversationId": conversationID})
	return nil
}

func audioPaths(userID, conversationID string, turnIndex int, assistantFormat string) (userPath, assistantPath string) {
	base := fmt.Sprintf("%s/%s/%d", userID, conversationID, turnIndex)
	return base + "/user.wav", base + "/assistant." + assistantFormat
}

func assistantMime(format string) string {
	if format == "mp3" {
		return "audio/mpeg"
	}
	return "audio/wav"
}

func (c *Conversations) SaveTurn(ctx context.Context, input SaveTurnInput) error {
	assistantFormat := input.AssistantFormat
	if assistantFormat == "" {
		assistantFormat = "wav"
	}

	var userPath, assistantPath string
	if len(input.UserWav) > 0 {
		userPath, assistantPath = audioPaths(input.UserID, input.ConversationID, input.TurnIndex, assistantFormat)
		if err := c.DB.UploadStorage(ctx, audioBucket, userPath, "audio/wav", input.UserWav); err != nil {
			log.Warn("user audio upload failed — saving transcript only", map[string]any{
				"conversationId": input.ConversationID,
				"turnIndex":      input.TurnIndex,
				"error":          err.Error(),
			})
			userPath = ""
		}
	}
	if len(input.AssistantAudio) > 0 && userPath != "" {
		_, assistantPath = audioPaths(input.UserID, input.ConversationID, input.TurnIndex, assistantFormat)
		if err := c.DB.UploadStorage(ctx, audioBucket, assistantPath, assistantMime(assistantFormat), input.AssistantAudio); err != nil {
			log.Warn("assistant audio upload failed — saving transcript only", map[string]any{
				"conversationId": input.ConversationID,
				"turnIndex":      input.TurnIndex,
				"error":          err.Error(),
			})
			assistantPath = ""
		}
	} else {
		assistantPath = ""
	}

	body := map[string]any{
		"conversation_id":      input.ConversationID,
		"turn_index":           input.TurnIndex,
		"user_transcript":      input.UserTranscript,
		"assistant_transcript": input.AssistantTranscript,
		"timings":              input.Timings,
	}
	if userPath != "" {
		body["user_audio_path"] = userPath
		body["user_audio_mime"] = "audio/wav"
	}
	if assistantPath != "" {
		body["assistant_audio_path"] = assistantPath
		body["assistant_audio_mime"] = assistantMime(assistantFormat)
	}
	if err := c.DB.Insert(ctx, "conversation_turns", body, nil); err != nil {
		return err
	}

	log.Print("turn saved", map[string]any{
		"conversationId":     input.ConversationID,
		"turnIndex":          input.TurnIndex,
		"userAudioPath":      userPath,
		"assistantAudioPath": assistantPath,
	})
	return nil
}

func (c *Conversations) PersistTurnAsync(input SaveTurnInput) {
	if !c.Enabled {
		return
	}
	go func() {
		if err := c.SaveTurn(context.Background(), input); err != nil {
			log.Warn("failed to persist turn", map[string]any{
				"conversationId": input.ConversationID,
				"turnIndex":      input.TurnIndex,
				"error":          err.Error(),
			})
		}
	}()
}

func (c *Conversations) EndAsync(conversationID string) {
	if !c.Enabled {
		return
	}
	go func() {
		if err := c.End(context.Background(), conversationID); err != nil {
			log.Warn("failed to end conversation", map[string]any{
				"conversationId": conversationID,
				"error":          err.Error(),
			})
		}
	}()
}
