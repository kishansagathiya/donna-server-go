package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type AudioChunk struct {
	Data   []byte
	Format string
}

type TTS struct {
	OpenAIKey      string
	CartesiaKey    string
	ElevenLabsKey  string
	Client         *http.Client
}

func NewTTS(openAI, cartesia, elevenLabs string) *TTS {
	return &TTS{
		OpenAIKey:     openAI,
		CartesiaKey:   cartesia,
		ElevenLabsKey: elevenLabs,
		Client:        &http.Client{Timeout: 120 * time.Second},
	}
}

func (t *TTS) SynthesizeSpeech(ctx context.Context, text string, onChunk func(AudioChunk) error) error {
	switch {
	case t.OpenAIKey != "":
		return t.streamOpenAI(ctx, text, onChunk)
	case t.CartesiaKey != "":
		return t.streamCartesia(ctx, text, onChunk)
	case t.ElevenLabsKey != "":
		return t.streamElevenLabs(ctx, text, onChunk)
	default:
		return fmt.Errorf("no TTS provider configured. Set OPENAI_API_KEY, CARTESIA_API_KEY, or ELEVENLABS_API_KEY")
	}
}

func (t *TTS) streamOpenAI(ctx context.Context, text string, onChunk func(AudioChunk) error) error {
	body, _ := json.Marshal(map[string]any{
		"model":           "tts-1",
		"voice":           "nova",
		"input":           text,
		"response_format": "mp3",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.OpenAIKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("OpenAI TTS %d: %s", res.StatusCode, string(b))
	}

	buf := make([]byte, 32*1024)
	for {
		n, readErr := res.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := onChunk(AudioChunk{Data: chunk, Format: "mp3"}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (t *TTS) streamCartesia(ctx context.Context, text string, onChunk func(AudioChunk) error) error {
	body, _ := json.Marshal(map[string]any{
		"model_id":   "sonic-3.5",
		"transcript": text,
		"voice":      map[string]any{"mode": "id", "id": "f786b574-daa5-4673-aa0c-cbe3e8534c02"},
		"output_format": map[string]any{
			"container":   "wav",
			"encoding":    "pcm_s16le",
			"sample_rate": 44100,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cartesia.ai/tts/bytes", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.CartesiaKey)
	req.Header.Set("Cartesia-Version", "2026-03-01")
	req.Header.Set("Content-Type", "application/json")

	res, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("Cartesia TTS %d: %s", res.StatusCode, string(b))
	}

	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	const chunkSize = 16 * 1024
	for offset := 0; offset < len(bytes); offset += chunkSize {
		end := offset + chunkSize
		if end > len(bytes) {
			end = len(bytes)
		}
		if err := onChunk(AudioChunk{Data: bytes[offset:end], Format: "wav"}); err != nil {
			return err
		}
	}
	return nil
}

func (t *TTS) streamElevenLabs(ctx context.Context, text string, onChunk func(AudioChunk) error) error {
	voiceID := "JBFqnCBsd6RMkjVDRZzb"
	body, _ := json.Marshal(map[string]any{
		"text":     text,
		"model_id": "eleven_turbo_v2_5",
	})
	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s?output_format=mp3_44100_128", voiceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("xi-api-key", t.ElevenLabsKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("ElevenLabs TTS %d: %s", res.StatusCode, string(b))
	}

	buf := make([]byte, 32*1024)
	for {
		n, readErr := res.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := onChunk(AudioChunk{Data: chunk, Format: "mp3"}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
