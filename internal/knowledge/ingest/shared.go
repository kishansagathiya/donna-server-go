package ingest

import (
	"regexp"
	"strings"
)

const MaxTextChars = 200_000
const MaxURLBytes = 2_000_000

func ClampText(text string) string {
	if len(text) <= MaxTextChars {
		return text
	}
	return text[:MaxTextChars] + "\n\n[truncated]"
}

func HTMLToText(html string) string {
	html = regexp.MustCompile(`(?is)<script[\s\S]*?</script>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?is)<style[\s\S]*?</style>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, " ")
	html = regexp.MustCompile(`\s+`).ReplaceAllString(html, " ")
	return strings.TrimSpace(html)
}
