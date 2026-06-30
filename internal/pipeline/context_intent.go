package pipeline

import (
	"regexp"
	"strings"
)

// personalPronounPattern matches first-person references that imply the answer
// should be tailored to the user's situation.
var personalPronounPattern = regexp.MustCompile(`(?i)\b(I|me|my|mine|myself|we|our|us|we're|I'm|I've|I'd|I'll)\b`)

// explicitMemoryPattern matches direct requests to use stored personal memory.
var explicitMemoryPattern = regexp.MustCompile(`(?i)\b(remember|recall|remind me|about me|do you know (about )?me|what did I|what do you (know|remember)|my name)\b`)

// NeedsUserContext reports whether a turn should load the user's profile and
// retrieve personal memory. Generic knowledge questions (no first-person or
// memory cues) return false so Donna answers without over-personalizing.
func NeedsUserContext(transcript string) bool {
	if looksLikeChitchat(transcript) {
		return false
	}
	t := strings.TrimSpace(transcript)
	if t == "" {
		return false
	}
	if explicitMemoryPattern.MatchString(t) {
		return true
	}
	if personalPronounPattern.MatchString(t) {
		return true
	}
	return false
}
