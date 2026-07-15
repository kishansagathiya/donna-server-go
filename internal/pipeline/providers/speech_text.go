package providers

import (
	"regexp"
	"strings"
)

var (
	markdownLinkRe = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	urlRe          = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"']+`)
	bareDomainRe   = regexp.MustCompile(`(?i)\b[a-z0-9][-a-z0-9]*(?:\.[a-z0-9][-a-z0-9]*)+(?:/[^\s]*)?`)
	altSlashRe     = regexp.MustCompile(`(\w+(?:[-']\w+)*)\s*/\s*(\w+(?:[-']\w+)*)`)
	extraSpaceRe   = regexp.MustCompile(`\s{2,}`)
)

// PrepareTextForSpeech rewrites assistant reply text before TTS so URLs are not
// read aloud and path-style slashes are spoken as "or" when they denote choices.
func PrepareTextForSpeech(text string) string {
	if text == "" {
		return text
	}

	out := markdownLinkRe.ReplaceAllString(text, "$1")
	out = urlRe.ReplaceAllString(out, "")
	out = bareDomainRe.ReplaceAllString(out, "")

	out = strings.ReplaceAll(out, "and/or", "and or")
	out = strings.ReplaceAll(out, "And/or", "and or")

	for altSlashRe.MatchString(out) {
		out = altSlashRe.ReplaceAllString(out, "$1 or $2")
	}

	out = extraSpaceRe.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}
