package pipeline

import (
	"regexp"
	"strings"
	"unicode"
)

type TranscriptClass string

const (
	TranscriptValid         TranscriptClass = "valid"
	TranscriptNoise         TranscriptClass = "noise"
	TranscriptFailedAttempt TranscriptClass = "failed_attempt"
)

const clearAttemptMinMS = 1500

var fillerWords = map[string]struct{}{
	"um": {}, "uh": {}, "uhh": {}, "umm": {}, "hmm": {}, "hm": {},
	"oh": {}, "ah": {}, "er": {}, "eh": {},
}

var hallucinationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)thank(s| you) for watching`),
	regexp.MustCompile(`(?i)\bsubscribe\b`),
	regexp.MustCompile(`(?i)\blike and subscribe\b`),
	regexp.MustCompile(`(?i)\bplease subscribe\b`),
	regexp.MustCompile(`(?i)\bmusic\b`),
	regexp.MustCompile(`(?i)\bapplause\b`),
	regexp.MustCompile(`(?i)\b\[music\]`),
	regexp.MustCompile(`(?i)\b\[applause\]`),
}

func ClassifyTranscript(transcript string, audio AudioQualityMeta) TranscriptClass {
	trimmed := strings.TrimSpace(transcript)
	normalized := normalizeTranscript(trimmed)

	if normalized == "" {
		if isClearSpeechAttempt(audio) {
			return TranscriptFailedAttempt
		}
		return TranscriptNoise
	}

	if len(normalized) < 3 {
		if isClearSpeechAttempt(audio) {
			return TranscriptFailedAttempt
		}
		return TranscriptNoise
	}

	if _, ok := fillerWords[normalized]; ok {
		if isClearSpeechAttempt(audio) {
			return TranscriptFailedAttempt
		}
		return TranscriptNoise
	}

	for _, pattern := range hallucinationPatterns {
		if pattern.MatchString(trimmed) {
			return TranscriptNoise
		}
	}

	if audio.DurationMs >= clearAttemptMinMS && meaningfulCharCount(normalized) < 3 {
		if isClearSpeechAttempt(audio) {
			return TranscriptFailedAttempt
		}
		return TranscriptNoise
	}

	return TranscriptValid
}

func normalizeTranscript(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '\'' {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func isClearSpeechAttempt(audio AudioQualityMeta) bool {
	return audio.DurationMs >= clearAttemptMinMS &&
		audio.SpeechMs >= 400 &&
		audio.PeakRms >= 0.01
}

func meaningfulCharCount(text string) int {
	count := 0
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			count++
		}
	}
	return count
}
