package memory

import (
	"fmt"
	"regexp"
	"strings"
)

// Hard-reject patterns for credentials and secrets.
var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(password|passwd|pwd)\b.{0,16}[:=]`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|secret[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|bearer)\b\s*[:=]`),
	regexp.MustCompile(`(?i)\b(sk-[a-zA-Z0-9]{16,}|ghp_[a-zA-Z0-9]{20,}|xox[baprs]-[a-zA-Z0-9-]{10,})\b`),
	regexp.MustCompile(`(?i)\b(private[_-]?key|ssh[_-]?key|-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----)`),
	regexp.MustCompile(`(?i)\b(credit[\s_-]?card|card[\s_-]?number|cvv|ssn|social[\s_-]?security)\b`),
}

// Protected traits that must never be stored when inferred (not explicitly stated
// by the user as a self-description they asked Donna to remember).
var protectedTraitPredicates = map[string]struct{}{
	"race": {}, "ethnicity": {}, "religion": {}, "political_affiliation": {},
	"sexual_orientation": {}, "gender_identity": {}, "disability": {},
	"health_condition": {}, "mental_health": {}, "immigration_status": {},
	"criminal_history": {}, "genetic_info": {},
}

var protectedTraitKeywords = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(race|ethnicity|ethnic|religion|religious|muslim|christian|jewish|hindu|buddhist|atheist)\b`),
	regexp.MustCompile(`(?i)\b(political|democrat|republican|liberal|conservative|libertarian)\b`),
	regexp.MustCompile(`(?i)\b(sexual orientation|gay|lesbian|bisexual|heterosexual|queer)\b`),
	regexp.MustCompile(`(?i)\b(disability|disabled|autism|adhd|depression|anxiety disorder|bipolar)\b`),
	regexp.MustCompile(`(?i)\b(immigration|undocumented|citizenship status)\b`),
}

// Candidate is a structured memory proposed by the extractor LLM.
type Candidate struct {
	Kind        string         `json:"kind"`
	Predicate   string         `json:"predicate"`
	Value       map[string]any `json:"value"`
	Fact        string         `json:"fact"`
	EntityName  string         `json:"entity_name"`
	Confidence  float64        `json:"confidence"`
	Sensitivity string         `json:"sensitivity"`
	Explicit    bool           `json:"explicit"`
	Ephemeral   bool           `json:"ephemeral"`
	ValidFrom   *string        `json:"valid_from"`
	ValidUntil  *string        `json:"valid_until"`
}

// RejectReason explains why a candidate was discarded by a hard filter.
type RejectReason string

const (
	RejectNone           RejectReason = ""
	RejectCredential     RejectReason = "credential"
	RejectProtectedTrait RejectReason = "inferred_protected_trait"
	RejectLowConfidence  RejectReason = "low_confidence"
	RejectEphemeral      RejectReason = "ephemeral"
	RejectEmpty          RejectReason = "empty"
)

// RejectUnsafe returns a reject reason when the candidate must never be stored.
func RejectUnsafe(c Candidate) RejectReason {
	fact := strings.TrimSpace(c.Fact)
	pred := strings.ToLower(strings.TrimSpace(c.Predicate))
	if fact == "" && pred == "" {
		return RejectEmpty
	}
	blob := fact + " " + pred + " " + valueBlob(c.Value)
	for _, re := range credentialPatterns {
		if re.MatchString(blob) {
			return RejectCredential
		}
	}
	if _, ok := protectedTraitPredicates[pred]; ok && !c.Explicit {
		return RejectProtectedTrait
	}
	if !c.Explicit {
		for _, re := range protectedTraitKeywords {
			if re.MatchString(blob) {
				return RejectProtectedTrait
			}
		}
	}
	return RejectNone
}

// ShouldDiscard applies confidence / ephemeral gates before reconcile.
func ShouldDiscard(c Candidate) RejectReason {
	if c.Ephemeral {
		return RejectEphemeral
	}
	if c.Confidence < 0.65 {
		return RejectLowConfidence
	}
	return RejectNone
}

// CanAutoActivate reports whether a safe candidate may be written as active
// without human review.
func CanAutoActivate(c Candidate, conflicting bool) bool {
	if RejectUnsafe(c) != RejectNone {
		return false
	}
	if ShouldDiscard(c) != RejectNone {
		return false
	}
	if conflicting {
		return false
	}
	if !c.Explicit {
		return false
	}
	sens := strings.ToLower(strings.TrimSpace(c.Sensitivity))
	if sens == "" {
		sens = "normal"
	}
	if sens != "normal" {
		return false
	}
	return c.Confidence >= 0.90
}

// NeedsReview reports mid-confidence, sensitive, or conflicting candidates.
func NeedsReview(c Candidate, conflicting bool) bool {
	if RejectUnsafe(c) != RejectNone || ShouldDiscard(c) != RejectNone {
		return false
	}
	if conflicting {
		return true
	}
	sens := strings.ToLower(strings.TrimSpace(c.Sensitivity))
	if sens == "sensitive" || sens == "restricted" {
		return true
	}
	if !c.Explicit {
		return true
	}
	return c.Confidence >= 0.65 && c.Confidence < 0.90
}

func valueBlob(v map[string]any) string {
	if v == nil {
		return ""
	}
	parts := make([]string, 0, len(v))
	for _, val := range v {
		parts = append(parts, strings.TrimSpace(fmt.Sprint(val)))
	}
	return strings.Join(parts, " ")
}
