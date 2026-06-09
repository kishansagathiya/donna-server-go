package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type STT struct {
	APIKey string
	Model  string
	Client *http.Client
}

func NewSTT(apiKey, model string) *STT {
	return &STT{
		APIKey: apiKey,
		Model:  model,
		Client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (s *STT) TranscribeAudio(ctx context.Context, audio []byte, format string) (string, int, error) {
	return s.transcribe(ctx, audio, format)
}

func (s *STT) TranscribeWAV(ctx context.Context, wav []byte) (string, int, error) {
	return s.transcribe(ctx, wav, "wav")
}

func (s *STT) transcribe(ctx context.Context, audio []byte, format string) (transcript string, ms int, err error) {
	start := time.Now()
	body, err := json.Marshal(map[string]any{
		"model": s.Model,
		"input_audio": map[string]string{
			"data":   base64.StdEncoding.EncodeToString(audio),
			"format": format,
		},
	})
	if err != nil {
		return "", 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterBase+"/audio/transcriptions", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.Client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return "", 0, fmt.Errorf("OpenRouter STT %d: %s", res.StatusCode, string(b))
	}

	var data struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return "", 0, err
	}

	return data.Text, int(time.Since(start).Milliseconds()), nil
}
