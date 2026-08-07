package knowledge

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type SourceSlice struct {
	ID        *string
	Content   string
	TurnIndex *int
}

// Explicit name intros — higher confidence than bare "I'm …".
var explicitNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:my name is|call me|name's)\s+([A-Za-z]+(?:\s+[A-Za-z]+)?)`),
}

// Soft intros: "I'm Sarah" / "I am Jordan Lee". Validated with looksLikePersonalName.
var softNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:i'm|i am)\s+([A-Za-z]+(?:\s+[A-Za-z]+)?)`),
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// Non-name first tokens after "I'm" / "I am" (progressive verbs, articles, etc.).
var nameDenylist = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "not": {}, "just": {}, "so": {}, "still": {},
	"here": {}, "there": {}, "back": {}, "done": {}, "ready": {}, "sorry": {},
	"fine": {}, "good": {}, "okay": {}, "ok": {}, "well": {}, "also": {},
	"going": {}, "gonna": {}, "wanna": {}, "gotta": {}, "trying": {}, "working": {},
	"looking": {}, "thinking": {}, "feeling": {}, "wondering": {}, "getting": {},
	"making": {}, "doing": {}, "having": {}, "being": {}, "coming": {}, "leaving": {},
	"watching": {}, "building": {}, "writing": {}, "reading": {}, "calling": {},
	"talking": {}, "walking": {}, "running": {}, "waiting": {}, "using": {},
	"planning": {}, "starting": {}, "finishing": {}, "checking": {}, "testing": {},
	"hearing": {}, "applying": {}, "listening": {}, "guessing": {}, "heading": {},
	"tracking": {}, "assuming": {}, "wonder": {}, "basically": {}, "mostly": {},
	"very": {}, "all": {}, "indian": {},
	"from": {}, "in": {}, "at": {}, "on": {}, "to": {}, "with": {}, "about": {},
}

func ExtractObviousFacts(sources []SourceSlice) []storage.NewFactInput {
	var facts []storage.NewFactInput
	seen := make(map[string]struct{})

	for _, source := range sources {
		for _, pattern := range explicitNamePatterns {
			match := pattern.FindStringSubmatch(source.Content)
			if len(match) < 2 {
				continue
			}
			name := strings.TrimSpace(match[1])
			if !looksLikePersonalName(name) {
				continue
			}
			appendNameFact(&facts, seen, name, source.ID)
		}

		for _, pattern := range softNamePatterns {
			match := pattern.FindStringSubmatch(source.Content)
			if len(match) < 2 {
				continue
			}
			name := strings.TrimSpace(match[1])
			if !looksLikePersonalName(name) {
				continue
			}
			appendNameFact(&facts, seen, name, source.ID)
		}

		for _, rawURL := range urlPattern.FindAllString(source.Content, -1) {
			u := strings.TrimRight(rawURL, ".,;:!?)")
			fact := "User shared link: " + u
			key := strings.ToLower(fact)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			facts = append(facts, storage.NewFactInput{
				Fact:     fact,
				Topic:    strPtr("links"),
				SourceID: source.ID,
			})
		}
	}
	return facts
}

func appendNameFact(facts *[]storage.NewFactInput, seen map[string]struct{}, name string, sourceID *string) {
	fact := "User's name is " + name
	key := strings.ToLower(fact)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	entity := name
	*facts = append(*facts, storage.NewFactInput{
		Fact:       fact,
		EntityName: &entity,
		Topic:      strPtr("identity"),
		SourceID:   sourceID,
	})
}

// looksLikePersonalName accepts 1–2 alphabetic tokens that look like a name,
// not progressive verbs ("building"), articles, or function words.
func looksLikePersonalName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	parts := strings.Fields(name)
	if len(parts) == 0 || len(parts) > 2 {
		return false
	}
	for i, part := range parts {
		if part == "" || !isAlphaWord(part) {
			return false
		}
		lower := strings.ToLower(part)
		if _, denied := nameDenylist[lower]; denied {
			return false
		}
		// Progressive "-ing" after I'm/I am is almost never a name.
		if i == 0 && strings.HasSuffix(lower, "ing") {
			return false
		}
	}
	return true
}

func isAlphaWord(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func strPtr(s string) *string { return &s }
