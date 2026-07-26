package protocol

import (
	"encoding/json"
	"fmt"
)

type TurnPhase string

const (
	TurnPhaseIdle         TurnPhase = "idle"
	TurnPhaseBusy         TurnPhase = "busy"
	TurnPhaseTranscribing TurnPhase = "transcribing"
	TurnPhaseGenerating   TurnPhase = "generating"
	TurnPhaseAnalyzing    TurnPhase = "analyzing"
	TurnPhaseFetching     TurnPhase = "fetching"
	TurnPhaseBrowsing     TurnPhase = "browsing"
	TurnPhaseSynthesizing TurnPhase = "synthesizing"
	TurnPhaseDone         TurnPhase = "done"
	TurnPhaseError        TurnPhase = "error"
)

type TurnTimings struct {
	STTMs           int `json:"sttMs"`
	AugmentMs       int `json:"augmentMs"`
	PreferencesMs   int `json:"preferencesMs"`
	PreLLMMs        int `json:"preLlmMs"`
	LLMFirstTokenMs int `json:"llmFirstTokenMs"`
	TTSFirstByteMs  int `json:"ttsFirstByteMs"`
	TotalMs         int `json:"totalMs"`
}

type ClientMessage struct {
	Type string `json:"type"`

	UserID    *string `json:"userId,omitempty"`
	SessionID *string `json:"sessionId,omitempty"`
	Mode      *string `json:"mode,omitempty"`
	// ClientNoteID, when set in notes mode, is used for idempotent note creates.
	ClientNoteID *string `json:"clientNoteId,omitempty"`

	Seq        *int    `json:"seq,omitempty"`
	Format     *string `json:"format,omitempty"`
	SampleRate *int    `json:"sampleRate,omitempty"`
	Channels   *int    `json:"channels,omitempty"`
	Data       *string `json:"data,omitempty"`
}

type ServerMessage struct {
	Type string `json:"type"`

	SessionID *string    `json:"sessionId,omitempty"`
	UserID    *string    `json:"userId,omitempty"`
	Phase     *TurnPhase `json:"phase,omitempty"`
	Text      *string    `json:"text,omitempty"`

	Seq         *int         `json:"seq,omitempty"`
	AudioFormat *string      `json:"format,omitempty"`
	AudioData   *string      `json:"data,omitempty"`
	SampleRate  *int         `json:"sampleRate,omitempty"`
	Channels    *int         `json:"channels,omitempty"`
	Timings     *TurnTimings `json:"timings,omitempty"`
	Skipped     *bool        `json:"skipped,omitempty"`
	NoteID      *string      `json:"noteId,omitempty"`
	Code        *string      `json:"code,omitempty"`
	Message     *string      `json:"message,omitempty"`
}

func ParseClientMessage(raw string) (*ClientMessage, error) {
	var msg ClientMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return nil, err
	}
	if msg.Type == "" {
		return nil, fmt.Errorf("message missing type")
	}
	return &msg, nil
}

func SerializeServerMessage(message ServerMessage) (string, error) {
	b, err := json.Marshal(message)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func SessionReady(sessionID, userID string) ServerMessage {
	return ServerMessage{
		Type:      "session.ready",
		SessionID: &sessionID,
		UserID:    &userID,
	}
}

func TurnPhaseMessage(phase TurnPhase) ServerMessage {
	return ServerMessage{
		Type:  "turn.phase",
		Phase: &phase,
	}
}

func ErrorMessage(code, message string) ServerMessage {
	return ServerMessage{
		Type:    "error",
		Code:    &code,
		Message: &message,
	}
}

func EmptyTurnTimings() TurnTimings {
	return TurnTimings{}
}

func TurnTranscript(text string) ServerMessage {
	return ServerMessage{Type: "turn.transcript", Text: &text}
}

func TurnReply(text string) ServerMessage {
	return ServerMessage{Type: "turn.reply", Text: &text}
}

func AudioOut(seq int, format, data string, sampleRate, channels int) ServerMessage {
	msg := ServerMessage{
		Type:        "audio.out",
		Seq:         &seq,
		AudioFormat: &format,
		AudioData:   &data,
	}
	if format == "pcm16" {
		msg.SampleRate = &sampleRate
		msg.Channels = &channels
	}
	return msg
}

func TurnDone(timings TurnTimings, skipped bool) ServerMessage {
	return TurnDoneWithNoteID(timings, skipped, "")
}

func TurnDoneWithNoteID(timings TurnTimings, skipped bool, noteID string) ServerMessage {
	msg := ServerMessage{
		Type:    "turn.done",
		Timings: &timings,
	}
	if skipped {
		msg.Skipped = &skipped
	}
	if noteID != "" {
		id := noteID
		msg.NoteID = &id
	}
	return msg
}
