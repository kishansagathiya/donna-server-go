package skills

import (
	"sort"
	"strings"
)

// MatchScore is one ranked skill match.
type MatchScore struct {
	Skill Skill
	Score float64
}

var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {},
	"but": {}, "by": {}, "for": {}, "from": {}, "how": {}, "i": {}, "in": {},
	"into": {}, "is": {}, "it": {}, "me": {}, "my": {}, "of": {}, "on": {},
	"or": {}, "that": {}, "the": {}, "this": {}, "to": {}, "up": {}, "using": {},
	"want": {}, "we": {}, "what": {}, "when": {}, "where": {}, "which": {},
	"with": {}, "you": {}, "your": {}, "can": {}, "do": {}, "does": {}, "find": {},
	"get": {}, "give": {}, "help": {}, "make": {}, "need": {}, "please": {},
	"show": {}, "tell": {}, "use": {},
}

func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~")
		if f == "" {
			continue
		}
		if _, ok := stopwords[f]; ok {
			continue
		}
		// Keep hyphenated skill names whole AND as parts ("web-research" →
		// "web-research", "web", "research") so goals phrased either way hit.
		out = append(out, f)
		for _, part := range strings.Split(f, "-") {
			if part != "" && part != f {
				out = append(out, part)
			}
		}
	}
	return out
}

// score computes overlap of goal tokens against the skill's name
// (weight 2) and description (weight 1). A token matching the full skill
// name counts extra.
func score(goalTokens []string, s Skill) float64 {
	nameTokens := tokenize(s.Name)
	descTokens := tokenize(s.Description)
	nameSet := map[string]struct{}{}
	for _, t := range nameTokens {
		nameSet[t] = struct{}{}
	}
	fullName := strings.ToLower(s.Name)
	descSet := map[string]struct{}{}
	for _, t := range descTokens {
		descSet[t] = struct{}{}
	}
	var score float64
	seen := map[string]struct{}{}
	for _, t := range goalTokens {
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		if _, ok := nameSet[t]; ok {
			score += 2
		}
		if _, ok := descSet[t]; ok {
			score += 1
		}
		if t == fullName {
			score += 3
		}
	}
	return score
}

// Match ranks skills against a goal text by token overlap. Only skills with a
// positive score are returned, best first, up to limit.
func Match(goal string, candidates []Skill, limit int) []MatchScore {
	goalTokens := tokenize(goal)
	if len(goalTokens) == 0 || len(candidates) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 5
	}
	scored := make([]MatchScore, 0, len(candidates))
	for _, s := range candidates {
		sc := score(goalTokens, s)
		if sc > 0 {
			scored = append(scored, MatchScore{Skill: s, Score: sc})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Skill.Name < scored[j].Skill.Name
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}
