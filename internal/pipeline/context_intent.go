package pipeline

import (
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/memory"
)

// NeedsUserContext reports whether a turn should load the user's profile and
// retrieve personal memory. Uses local memory planning (cues, entities,
// temporal + source-recall intent) instead of a first-person-only regex gate.
// Generic knowledge questions return false so Donna answers without
// over-personalizing and without embedding requests.
func NeedsUserContext(transcript string) bool {
	return memory.PlanMemory(transcript).ShouldRetrieve
}

// MemoryPlanFor returns the full retrieval plan for a transcript.
func MemoryPlanFor(transcript string) memory.Plan {
	return memory.PlanMemory(strings.TrimSpace(transcript))
}
