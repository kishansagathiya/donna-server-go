package connectors

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

const (
	// TargetChunkChars is the preferred transcript chunk size.
	TargetChunkChars = 8000
	// MaxChunkChars is the hard maximum per kb_sources transcript chunk.
	MaxChunkChars = 12000
)

// ContentHash returns a hex SHA-256 of normalized content for idempotency.
func ContentHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strings.TrimSpace(p)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ChunkTranscript splits speaker-aware transcript text into chunks near the
// target size without exceeding the hard maximum. Prefers breaks at blank
// lines, then newlines, then spaces.
func ChunkTranscript(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if utf8.RuneCountInString(text) <= MaxChunkChars {
		if utf8.RuneCountInString(text) <= TargetChunkChars {
			return []string{text}
		}
	}

	var chunks []string
	runes := []rune(text)
	for len(runes) > 0 {
		if len(runes) <= MaxChunkChars {
			chunks = append(chunks, strings.TrimSpace(string(runes)))
			break
		}
		limit := TargetChunkChars
		if limit > len(runes) {
			limit = len(runes)
		}
		window := runes[:min(len(runes), MaxChunkChars)]
		cut := findBreak(window, limit)
		if cut <= 0 {
			cut = min(len(runes), MaxChunkChars)
		}
		chunk := strings.TrimSpace(string(runes[:cut]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[cut:]
		// Skip leading whitespace on the next chunk.
		for len(runes) > 0 && (runes[0] == '\n' || runes[0] == ' ' || runes[0] == '\t' || runes[0] == '\r') {
			runes = runes[1:]
		}
	}
	return chunks
}

func findBreak(window []rune, prefer int) int {
	if prefer > len(window) {
		prefer = len(window)
	}
	// Prefer double newline near target.
	best := -1
	searchFrom := prefer
	if searchFrom > len(window) {
		searchFrom = len(window)
	}
	for i := searchFrom; i >= prefer/2; i-- {
		if i >= 2 && window[i-1] == '\n' && window[i-2] == '\n' {
			best = i
			break
		}
	}
	if best > 0 {
		return best
	}
	for i := searchFrom; i >= prefer/2; i-- {
		if window[i-1] == '\n' {
			return i
		}
	}
	for i := searchFrom; i >= prefer/2; i-- {
		if window[i-1] == ' ' {
			return i
		}
	}
	if prefer > 0 {
		return prefer
	}
	return len(window)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
