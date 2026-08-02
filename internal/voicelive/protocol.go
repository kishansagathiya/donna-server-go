package voicelive

import "encoding/json"

// Client → Donna
const (
	ClientSessionStart = "session.start"
	ClientAudioChunk   = "audio.chunk"
	ClientSessionEnd   = "session.end"
)

// Donna → Client
const (
	ServerSessionReady = "session.ready"
	ServerAudioChunk   = "audio.chunk"
	ServerTranscript   = "transcript"
	ServerInterrupted  = "interrupted"
	ServerError        = "error"
	ServerSessionEnded = "session.ended"
)

type ClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
}

type ServerMessage struct {
	Type       string `json:"type"`
	SessionID  string `json:"sessionId,omitempty"`
	Data       string `json:"data,omitempty"`
	SampleRate int    `json:"sampleRate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	Role       string `json:"role,omitempty"`
	Text       string `json:"text,omitempty"`
	Final      bool   `json:"final,omitempty"`
	Message    string `json:"message,omitempty"`
}

func parseClientMessage(raw []byte) (ClientMessage, error) {
	var msg ClientMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return ClientMessage{}, err
	}
	return msg, nil
}
