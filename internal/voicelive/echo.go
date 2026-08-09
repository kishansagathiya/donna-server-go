package voicelive

import (
	"regexp"
	"strings"
)

var nonWordRE = regexp.MustCompile(`[^\p{L}\p{N}\s]+`)

func normalizeTranscript(text string) string {
	text = strings.ToLower(text)
	text = strings.Map(func(r rune) rune {
		if r == '\'' || r == '’' {
			return -1
		}
		return r
	}, text)
	text = nonWordRE.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

// looksLikeEcho reports whether a user caption is likely Donna's own speech
// captured by the microphone (speaker echo / loopback).
func looksLikeEcho(userText, assistantText string) bool {
	user := normalizeTranscript(userText)
	assistant := normalizeTranscript(assistantText)
	if user == "" || assistant == "" {
		return false
	}
	if user == assistant {
		return true
	}

	userWords := strings.Fields(user)
	assistantWords := strings.Fields(assistant)
	if len(userWords) < 3 {
		return false
	}
	if strings.Contains(assistant, user) {
		return true
	}
	if strings.Contains(user, assistant) && len(assistantWords) >= 3 {
		return true
	}

	assistantSet := make(map[string]struct{}, len(assistantWords))
	for _, w := range assistantWords {
		assistantSet[w] = struct{}{}
	}
	overlap := 0
	for _, w := range userWords {
		if _, ok := assistantSet[w]; ok {
			overlap++
		}
	}
	den := len(userWords)
	if len(assistantWords) > den {
		den = len(assistantWords)
	}
	if den == 0 {
		return false
	}
	ratio := float64(overlap) / float64(den)
	return overlap >= 3 && ratio >= 0.65
}
