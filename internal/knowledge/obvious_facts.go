package knowledge

import (
	"regexp"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type SourceSlice struct {
	ID        *string
	Content   string
	TurnIndex *int
}

var namePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:my name is|i'm|i am|call me|name's)\s+([A-Za-z]+(?:\s+[A-Za-z]+)?)`),
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

func ExtractObviousFacts(sources []SourceSlice) []storage.NewFactInput {
	var facts []storage.NewFactInput
	seen := make(map[string]struct{})

	for _, source := range sources {
		for _, pattern := range namePatterns {
			match := pattern.FindStringSubmatch(source.Content)
			if len(match) < 2 {
				continue
			}
			name := strings.TrimSpace(match[1])
			fact := "User's name is " + name
			key := strings.ToLower(fact)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			entity := name
			facts = append(facts, storage.NewFactInput{
				Fact:       fact,
				EntityName: &entity,
				Topic:      strPtr("identity"),
				SourceID:   source.ID,
			})
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

func strPtr(s string) *string { return &s }
