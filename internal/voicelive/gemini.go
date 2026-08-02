package voicelive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

const geminiLiveWSURL = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"

type geminiClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type geminiSetup struct {
	Setup geminiSetupBody `json:"setup"`
}

type geminiSetupBody struct {
	Model                    string                     `json:"model"`
	GenerationConfig         geminiGenerationConfig     `json:"generationConfig"`
	SystemInstruction        geminiContent              `json:"systemInstruction"`
	Tools                    []geminiTool               `json:"tools,omitempty"`
	RealtimeInputConfig      *geminiRealtimeInputConfig `json:"realtimeInputConfig,omitempty"`
	InputAudioTranscription  *geminiEmptyObj            `json:"inputAudioTranscription,omitempty"`
	OutputAudioTranscription *geminiEmptyObj            `json:"outputAudioTranscription,omitempty"`
}

type geminiRealtimeInputConfig struct {
	AutomaticActivityDetection *geminiAutoActivity `json:"automaticActivityDetection,omitempty"`
}

type geminiAutoActivity struct {
	EndOfSpeechSensitivity string `json:"endOfSpeechSensitivity,omitempty"`
	SilenceDurationMs      int    `json:"silenceDurationMs,omitempty"`
}

type geminiEmptyObj struct{}

type geminiGenerationConfig struct {
	ResponseModalities []string            `json:"responseModalities"`
	SpeechConfig       *geminiSpeechConfig `json:"speechConfig,omitempty"`
}

type geminiSpeechConfig struct {
	VoiceConfig geminiVoiceConfig `json:"voiceConfig"`
}

type geminiVoiceConfig struct {
	PrebuiltVoiceConfig geminiPrebuiltVoice `json:"prebuiltVoiceConfig"`
}

type geminiPrebuiltVoice struct {
	VoiceName string `json:"voiceName"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text         string        `json:"text,omitempty"`
	InlineData   *geminiBlob   `json:"inlineData,omitempty"`
	FunctionCall *geminiFnCall `json:"functionCall,omitempty"`
}

type geminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFnDecl `json:"functionDeclarations"`
}

type geminiFnDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiFnCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiServerMessage struct {
	SetupComplete *struct{} `json:"setupComplete"`
	ServerContent *struct {
		ModelTurn *struct {
			Parts []geminiPart `json:"parts"`
		} `json:"modelTurn"`
		InputTranscription *struct {
			Text string `json:"text"`
		} `json:"inputTranscription"`
		OutputTranscription *struct {
			Text string `json:"text"`
		} `json:"outputTranscription"`
		Interrupted  bool `json:"interrupted"`
		TurnComplete bool `json:"turnComplete"`
	} `json:"serverContent"`
	ToolCall *struct {
		FunctionCalls []geminiFnCall `json:"functionCalls"`
	} `json:"toolCall"`
	ToolCallCancellation *struct {
		IDs []string `json:"ids"`
	} `json:"toolCallCancellation"`
}

func dialGemini(ctx context.Context, apiKey string) (*geminiClient, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("missing GEMINI_API_KEY")
	}
	u, err := url.Parse(geminiLiveWSURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("key", apiKey)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.Dial(ctx, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("gemini dial: %w", err)
	}
	conn.SetReadLimit(8 << 20)
	return &geminiClient{conn: conn}, nil
}

func (g *geminiClient) sendJSON(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.conn.Write(ctx, websocket.MessageText, data)
}

func (g *geminiClient) setup(ctx context.Context, model, systemPrompt, voiceName string) error {
	if !strings.HasPrefix(model, "models/") {
		model = "models/" + model
	}
	if voiceName == "" {
		voiceName = "Aoede"
	}
	msg := geminiSetup{
		Setup: geminiSetupBody{
			Model: model,
			GenerationConfig: geminiGenerationConfig{
				ResponseModalities: []string{"AUDIO"},
				SpeechConfig: &geminiSpeechConfig{
					VoiceConfig: geminiVoiceConfig{
						PrebuiltVoiceConfig: geminiPrebuiltVoice{VoiceName: voiceName},
					},
				},
			},
			SystemInstruction: geminiContent{
				Parts: []geminiPart{{Text: systemPrompt}},
			},
			Tools: []geminiTool{{
				FunctionDeclarations: []geminiFnDecl{{
					Name:        "retrieve_memory",
					Description: "Look up durable facts, preferences, people, projects, and notes about this user from Donna's memory. Use whenever the answer may depend on prior context about the user.",
					Parameters: map[string]any{
						"type": "OBJECT",
						"properties": map[string]any{
							"query": map[string]any{
								"type":        "STRING",
								"description": "Short search query describing what to recall",
							},
						},
						"required": []string{"query"},
					},
				}},
			}},
			// Prefer waiting through thinking pauses over cutting the user off early.
			RealtimeInputConfig: &geminiRealtimeInputConfig{
				AutomaticActivityDetection: &geminiAutoActivity{
					EndOfSpeechSensitivity: "END_SENSITIVITY_LOW",
					SilenceDurationMs:      1200,
				},
			},
			InputAudioTranscription:  &geminiEmptyObj{},
			OutputAudioTranscription: &geminiEmptyObj{},
		},
	}
	return g.sendJSON(ctx, msg)
}

func (g *geminiClient) sendAudio(ctx context.Context, pcmBase64 string) error {
	return g.sendJSON(ctx, map[string]any{
		"realtimeInput": map[string]any{
			"audio": map[string]any{
				"mimeType": "audio/pcm;rate=16000",
				"data":     pcmBase64,
			},
		},
	})
}

func (g *geminiClient) sendToolResponse(ctx context.Context, id, name string, response map[string]any) error {
	return g.sendJSON(ctx, map[string]any{
		"toolResponse": map[string]any{
			"functionResponses": []map[string]any{{
				"id":       id,
				"name":     name,
				"response": response,
			}},
		},
	})
}

func (g *geminiClient) read(ctx context.Context) (geminiServerMessage, error) {
	_, data, err := g.conn.Read(ctx)
	if err != nil {
		return geminiServerMessage{}, err
	}
	var msg geminiServerMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return geminiServerMessage{}, err
	}
	return msg, nil
}

func (g *geminiClient) close() {
	if g == nil || g.conn == nil {
		return
	}
	_ = g.conn.Close(websocket.StatusNormalClosure, "")
}
