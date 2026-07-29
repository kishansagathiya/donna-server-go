package tts

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/wav"
)

const maxTTSRunes = 5_000

type Handler struct {
	TTS *providers.TTS
}

type requestBody struct {
	Text string `json:"text"`
}

// ServeHTTP synthesizes speech for assistant text (POST /tts).
// Response is a complete audio file: audio/wav (OpenAI/Cartesia) or audio/mpeg (ElevenLabs).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	if h.TTS == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "tts_unavailable",
			"message": "No TTS provider configured",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	defer r.Body.Close()

	var body requestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	text := providers.PrepareTextForSpeech(strings.TrimSpace(body.Text))
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty_text"})
		return
	}
	if utf8.RuneCountInString(text) > maxTTSRunes {
		runes := []rune(text)
		text = string(runes[:maxTTSRunes])
	}

	var (
		format     string
		sampleRate = 24_000
		channels   = 1
		pcm        []byte
		encoded    []byte
	)

	err := h.TTS.SynthesizeSpeech(r.Context(), text, func(chunk providers.AudioChunk) error {
		if format == "" {
			format = chunk.Format
			if chunk.SampleRate > 0 {
				sampleRate = chunk.SampleRate
			}
			if chunk.Channels > 0 {
				channels = chunk.Channels
			}
		}
		if chunk.Format == "pcm16" {
			pcm = append(pcm, chunk.Data...)
			return nil
		}
		encoded = append(encoded, chunk.Data...)
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "tts_failed",
			"message": err.Error(),
		})
		return
	}

	switch format {
	case "pcm16":
		out := wav.PCM16ToWAV(pcm, wav.PCMFormat{SampleRate: sampleRate, Channels: channels})
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
	case "wav":
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	case "mp3":
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "tts_failed",
			"message": "unknown audio format",
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
