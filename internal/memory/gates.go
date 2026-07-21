package memory

// Notes & Memory V2 evaluation gates from epic DoD (#165).
// Fixtures: donna-server-go/evals/notes_memory_v2_scenarios.json
// Docs: docs/notes-memory-v2/gates-slos-rollout.md
// Related (do not reparent): #117 evals, #118 SLOs, #119 retrieval logging.
const (
	GateNotesCacheRenderP95Ms     = 100
	GateSmartTagAutoPrecision     = 0.90
	GateExtractionHighConfPrecision = 0.95
	GateDurableRecall             = 0.90
	GateMemoryRetrievalP95Ms      = 500

	// Sync LLM / embeddings must not run on the Notes write request path.
	GateNotesWriteAllowsSyncLLM       = false
	GateNotesWriteAllowsSyncEmbedding = false

	// Generic / chit-chat prompts must not trigger embedding retrieval.
	GateGenericPromptAllowsEmbedding = false

	// Sensitive / restricted memories require human review before activation.
	GateSensitiveRequiresReview = true
	GateAllowCredentialStorage  = false
	GateAllowProtectedTraits    = false
)

// RolloutStage describes staged rollout cohorts (#165).
type RolloutStage string

const (
	RolloutInternal RolloutStage = "internal"
	RolloutPct5     RolloutStage = "5pct"
	RolloutPct25    RolloutStage = "25pct"
	RolloutPct100   RolloutStage = "100pct"
)

// RolloutCohortSalt keeps cohort membership stable across expansions.
const RolloutCohortSalt = "notes-memory-v2-2026"

// InRolloutCohort reports whether userID belongs to the given stage via stable hashing.
// Stages nest: 5% ⊂ 25% ⊂ 100%. Internal is allow-list only (always false here).
func InRolloutCohort(userID string, stage RolloutStage) bool {
	if userID == "" {
		return false
	}
	switch stage {
	case RolloutPct100:
		return true
	case RolloutPct25:
		return stableBucket(userID) < 25
	case RolloutPct5:
		return stableBucket(userID) < 5
	case RolloutInternal:
		return false
	default:
		return false
	}
}

func stableBucket(userID string) int {
	// FNV-1a 32-bit over salt+userID → [0,100)
	const (
		offset = 2166136261
		prime  = 16777619
	)
	h := uint32(offset)
	for i := 0; i < len(RolloutCohortSalt); i++ {
		h ^= uint32(RolloutCohortSalt[i])
		h *= prime
	}
	for i := 0; i < len(userID); i++ {
		h ^= uint32(userID[i])
		h *= prime
	}
	return int(h % 100)
}
