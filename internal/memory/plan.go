package memory

import (
	"regexp"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Route identifies which memory subsystem a plan should query.
type Route string

const (
	RouteIdentityPrefs Route = "identity_prefs"
	RouteGoalsProjects Route = "goals_projects"
	RouteEpisodic      Route = "episodic"
)

// Plan is a local (no-LLM) memory retrieval plan for a user turn.
type Plan struct {
	ShouldRetrieve bool     `json:"should_retrieve"`
	NeedsEmbed     bool     `json:"needs_embed"`
	Routes         []Route  `json:"routes"`
	Entities       []string `json:"entities"`
	Temporal       bool     `json:"temporal"`
	SourceRecall   bool     `json:"source_recall"`
	Cues           []string `json:"cues"`
}

var (
	personalPronounRe = regexp.MustCompile(`(?i)\b(I|me|my|mine|myself|we|our|us|we're|I'm|I've|I'd|I'll)\b`)
	explicitMemoryRe  = regexp.MustCompile(`(?i)\b(remember|recall|remind me|about me|do you know (about )?me|what did I|what do you (know|remember)|my name)\b`)
	temporalRe        = regexp.MustCompile(`(?i)\b(when|what day|what date|birthday|anniversary|deadline|due|schedule|tomorrow|yesterday|last week|next week|ago)\b`)
	sourceRecallRe    = regexp.MustCompile(`(?i)\b(in my notes?|from (my|the) notes?|that (meeting|call|conversation|doc|document|email|transcript)|what did .+ say|according to)\b`)
	goalProjectRe     = regexp.MustCompile(`(?i)\b(goal|goals|project|projects|roadmap|deadline|milestone|okr|launch|ship)\b`)
	identityPrefRe    = regexp.MustCompile(`(?i)\b(name|prefer|preference|likes?|dislikes?|allergic|allergy|live|lives|based|timezone|pronouns?)\b`)
	entityTokenRe     = regexp.MustCompile(`\b([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\b`)
)

// known low-signal greetings/acknowledgements (mirrors pipeline chitchat set).
var planChitchat = map[string]struct{}{
	"hey": {}, "hi": {}, "hello": {}, "yo": {}, "howdy": {}, "sup": {},
	"ok": {}, "okay": {}, "k": {}, "kk": {}, "cool": {}, "nice": {},
	"great": {}, "awesome": {}, "perfect": {}, "sounds good": {}, "got it": {},
	"thanks": {}, "thank you": {}, "thx": {}, "ty": {}, "cheers": {},
	"yeah": {}, "yes": {}, "yep": {}, "yup": {}, "no": {}, "nope": {},
	"lol": {}, "haha": {}, "ha": {}, "lmao": {}, "wow": {}, "ohh": {},
	"hmm": {}, "oh": {}, "ah": {}, "ugh": {}, "right": {}, "sure": {},
	"bye": {}, "goodbye": {}, "gm": {}, "good morning": {}, "good night": {},
	"gn": {}, "what's up": {}, "wassup": {},
}

// PlanMemory builds a retrieval plan from local cues. Generic knowledge prompts
// return ShouldRetrieve=false and NeedsEmbed=false (no embedding request).
func PlanMemory(transcript string) Plan {
	t := strings.TrimSpace(transcript)
	if t == "" || isPlanChitchat(t) {
		return Plan{}
	}

	p := Plan{}
	lower := strings.ToLower(t)

	if personalPronounRe.MatchString(t) {
		p.Cues = append(p.Cues, "personal_pronoun")
	}
	if explicitMemoryRe.MatchString(t) {
		p.Cues = append(p.Cues, "explicit_memory")
		p.SourceRecall = true
	}
	if temporalRe.MatchString(t) {
		p.Temporal = true
		p.Cues = append(p.Cues, "temporal")
	}
	if sourceRecallRe.MatchString(t) {
		p.SourceRecall = true
		p.Cues = append(p.Cues, "source_recall")
	}
	p.Entities = extractEntities(t)
	if len(p.Entities) > 0 {
		p.Cues = append(p.Cues, "entity")
	}

	routeIdentity := identityPrefRe.MatchString(lower) || personalPronounRe.MatchString(t) || explicitMemoryRe.MatchString(t)
	routeGoals := goalProjectRe.MatchString(lower)
	routeEpisodic := p.SourceRecall || (p.Temporal && len(p.Entities) > 0) || (len(p.Entities) > 0 && !isGenericKnowledge(lower))

	// Birthday-style: "When is Sarah's birthday?" → entity + temporal → episodic + identity/relationship prefs.
	if p.Temporal && len(p.Entities) > 0 {
		routeEpisodic = true
		routeIdentity = true
	}

	if routeIdentity {
		p.Routes = append(p.Routes, RouteIdentityPrefs)
	}
	if routeGoals {
		p.Routes = append(p.Routes, RouteGoalsProjects)
	}
	if routeEpisodic {
		p.Routes = append(p.Routes, RouteEpisodic)
	}

	p.ShouldRetrieve = len(p.Routes) > 0 || len(p.Cues) > 0
	// Generic encyclopedic prompts: no personal cues, no entities of interest.
	if isGenericKnowledge(lower) && !personalPronounRe.MatchString(t) && !explicitMemoryRe.MatchString(t) && len(p.Entities) == 0 {
		p.ShouldRetrieve = false
		p.Routes = nil
		p.Cues = nil
	}

	p.NeedsEmbed = p.ShouldRetrieve
	return p
}

func isPlanChitchat(transcript string) bool {
	normalized := strings.ToLower(strings.TrimSpace(transcript))
	normalized = strings.TrimSpace(strings.Trim(normalized, "!?.,~"))
	_, ok := planChitchat[normalized]
	return ok
}

func isGenericKnowledge(lower string) bool {
	genericStarts := []string{
		"what is a ", "what is an ", "what's a ", "what's an ",
		"explain ", "how does ", "how do ", "define ", "compare ",
		"write a ", "draft ", "summarize the following",
	}
	for _, g := range genericStarts {
		if strings.HasPrefix(lower, g) {
			return true
		}
	}
	// Common non-personal technical questions without personal markers.
	if strings.Contains(lower, " what is ") || strings.HasPrefix(lower, "what is ") {
		if !strings.Contains(lower, " my ") && !strings.Contains(lower, " me ") {
			return true
		}
	}
	return false
}

func extractEntities(transcript string) []string {
	// Prefer capitalized proper nouns; filter sentence-start noise.
	matches := entityTokenRe.FindAllString(transcript, -1)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(matches))
	skip := map[string]struct{}{
		"I": {}, "When": {}, "What": {}, "Where": {}, "Who": {}, "Why": {}, "How": {},
		"The": {}, "A": {}, "An": {}, "My": {}, "Your": {}, "Is": {}, "Are": {}, "Do": {},
		"Does": {}, "Did": {}, "Can": {}, "Could": {}, "Would": {}, "Should": {}, "Tell": {},
		"Remind": {}, "Please": {}, "Thanks": {},
	}
	for _, m := range matches {
		if _, bad := skip[m]; bad {
			continue
		}
		// Single-letter or all-caps acronyms longer than 1 are ok; drop pure particles.
		if len(m) < 2 {
			continue
		}
		// Reject if the token is mid-sentence lowercase start (regex already requires capital).
		key := strings.ToLower(m)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, m)
		if len(out) >= 6 {
			break
		}
	}
	// Possessive forms: Sarah's → Sarah
	for i, e := range out {
		if strings.HasSuffix(e, "'s") || strings.HasSuffix(e, "’s") {
			out[i] = strings.TrimSuffix(strings.TrimSuffix(e, "'s"), "’s")
		}
	}
	// Also catch "Sarah's" when regex missed due to apostrophe.
	possessive := regexp.MustCompile(`\b([A-Z][a-z]+)'s\b`)
	for _, m := range possessive.FindAllStringSubmatch(transcript, -1) {
		if len(m) < 2 {
			continue
		}
		name := m[1]
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		if _, bad := skip[name]; bad {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

// KindsForRoute maps a retrieval route to kb memory_kind filters.
func KindsForRoute(r Route) []string {
	switch r {
	case RouteIdentityPrefs:
		return []string{
			storage.MemoryKindIdentity,
			storage.MemoryKindPreference,
			storage.MemoryKindRelationship,
			storage.MemoryKindHabit,
			storage.MemoryKindLocation,
		}
	case RouteGoalsProjects:
		return []string{storage.MemoryKindGoal, storage.MemoryKindProject}
	case RouteEpisodic:
		return []string{storage.MemoryKindEvent, storage.MemoryKindFact, storage.MemoryKindRelationship, storage.MemoryKindOther}
	default:
		return nil
	}
}
