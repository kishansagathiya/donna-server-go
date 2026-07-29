package tts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/wav"
)

const (
	maxTTSRunes    = 5_000
	ttsAudioBucket = "conversation-audio"
)

// Store persists synthesized reply audio for reuse across sessions.
type Store interface {
	Enabled() bool
	DownloadStorage(ctx context.Context, bucket, path string) ([]byte, error)
	UpsertStorage(ctx context.Context, bucket, path, contentType string, data []byte) error
}

type Handler struct {
	TTS   *providers.TTS
	Store Store
}

type requestBody struct {
	Text string `json:"text"`
}

// ServeHTTP synthesizes speech for assistant text (POST /tts).
// Audio is stored under conversation-audio/{userID}/tts/{hash}.{ext} and reused
// on later requests for the same user + voice + text.
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

	userID, _ := appauth.UserIDFromContext(r.Context())

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

	format := h.TTS.PreferredFormat()
	contentType := mimeForFormat(format)
	cachePath := ""
	if userID != "" && h.Store != nil && h.Store.Enabled() {
		cachePath = objectPath(userID, h.TTS.CacheFingerprint(), text, format)
		if cached, err := h.Store.DownloadStorage(r.Context(), ttsAudioBucket, cachePath); err == nil && len(cached) > 0 {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "private, max-age=3600")
			w.Header().Set("X-Donna-TTS-Cache", "hit")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		}
	}

	var (
		outFormat  string
		sampleRate = 24_000
		channels   = 1
		pcm        []byte
		encoded    []byte
	)

	err := h.TTS.SynthesizeSpeech(r.Context(), text, func(chunk providers.AudioChunk) error {
		if outFormat == "" {
			outFormat = chunk.Format
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

	var audio []byte
	switch outFormat {
	case "pcm16":
		audio = wav.PCM16ToWAV(pcm, wav.PCMFormat{SampleRate: sampleRate, Channels: channels})
		contentType = "audio/wav"
		format = "wav"
	case "wav":
		audio = encoded
		contentType = "audio/wav"
		format = "wav"
	case "mp3":
		audio = encoded
		contentType = "audio/mpeg"
		format = "mp3"
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "tts_failed",
			"message": "unknown audio format",
		})
		return
	}

	if cachePath != "" && len(audio) > 0 {
		// Recompute path if format diverged from PreferredFormat.
		cachePath = objectPath(userID, h.TTS.CacheFingerprint(), text, format)
		if err := h.Store.UpsertStorage(r.Context(), ttsAudioBucket, cachePath, contentType, audio); err != nil {
			log.Warn("tts cache upload failed", map[string]any{
				"path":  cachePath,
				"error": err.Error(),
			})
		}
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Donna-TTS-Cache", "miss")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

func objectPath(userID, fingerprint, text, format string) string {
	sum := sha256.Sum256([]byte(fingerprint + "\n" + text))
	ext := "wav"
	if format == "mp3" {
		ext = "mp3"
	}
	return userID + "/tts/" + hex.EncodeToString(sum[:]) + "." + ext
}

func mimeForFormat(format string) string {
	if format == "mp3" {
		return "audio/mpeg"
	}
	return "audio/wav"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
