package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

const (
	MemoryKindIdentity     = "identity"
	MemoryKindPreference   = "preference"
	MemoryKindRelationship = "relationship"
	MemoryKindGoal         = "goal"
	MemoryKindProject      = "project"
	MemoryKindHabit        = "habit"
	MemoryKindRoutine      = "routine"
	MemoryKindLocation     = "location"
	MemoryKindEvent        = "event"
	MemoryKindFact         = "fact"
	MemoryKindOther        = "other"
	MemoryKindConstraint   = "constraint"
	MemoryKindInstruction  = "instruction"

	SensitivityNormal     = "normal"
	SensitivitySensitive  = "sensitive"
	SensitivityRestricted = "restricted"

	ReviewActive        = "active"
	ReviewPendingReview = "pending_review"
	ReviewRejected      = "rejected"
	ReviewSuperseded    = "superseded"
	ReviewOutdated      = "outdated"

	EvidenceConversationTurn = "conversation_turn"
	EvidenceNote             = "note"
	EvidenceKBSource         = "kb_source"
	EvidenceIntegrationItem  = "integration_item"
	EvidenceManual           = "manual"

	SuggestionKindMemory = "memory"
	SuggestionPending    = "pending"
	SuggestionAccepted   = "accepted"
	SuggestionRejected   = "rejected"

	FeedbackConfirm     = "confirm"
	FeedbackReject      = "reject"
	FeedbackEdit        = "edit"
	FeedbackNotRelevant = "not_relevant"
	FeedbackOutdated    = "outdated"
	FeedbackAccept      = "accept"
	FeedbackResolve     = "resolve"
)

// MemoryUIGroupOrder is the grouped Memory UI section order (#164).
var MemoryUIGroupOrder = []string{
	MemoryKindIdentity,
	MemoryKindPreference,
	MemoryKindRelationship,
	MemoryKindProject,
	MemoryKindGoal,
	MemoryKindRoutine,
	MemoryKindEvent,
	MemoryKindConstraint,
	MemoryKindInstruction,
}

// NormalizeMemoryKindForUI maps storage kinds onto UI group keys.
func NormalizeMemoryKindForUI(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case MemoryKindIdentity:
		return MemoryKindIdentity
	case MemoryKindPreference:
		return MemoryKindPreference
	case MemoryKindRelationship:
		return MemoryKindRelationship
	case MemoryKindProject:
		return MemoryKindProject
	case MemoryKindGoal:
		return MemoryKindGoal
	case MemoryKindHabit, MemoryKindRoutine:
		return MemoryKindRoutine
	case MemoryKindEvent:
		return MemoryKindEvent
	case MemoryKindConstraint:
		return MemoryKindConstraint
	case MemoryKindInstruction:
		return MemoryKindInstruction
	default:
		return MemoryKindOther
	}
}

// MemoryFact is a kb_facts row including Notes/Memory V2 structured columns.
type MemoryFact struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id"`
	Fact         string         `json:"fact"`
	EntityName   *string        `json:"entity_name,omitempty"`
	Topic        *string        `json:"topic,omitempty"`
	SourceID     *string        `json:"source_id,omitempty"`
	SupersedesID *string        `json:"supersedes_id,omitempty"`
	Active       bool           `json:"active"`
	CreatedAt    string         `json:"created_at,omitempty"`
	ContentVersion int64        `json:"content_version,omitempty"`
	MemoryKind   *string        `json:"memory_kind,omitempty"`
	Predicate    *string        `json:"predicate,omitempty"`
	ObjectValue  map[string]any `json:"object_value,omitempty"`
	Confidence   *float64       `json:"confidence,omitempty"`
	Sensitivity  string         `json:"sensitivity,omitempty"`
	ValidFrom    *string        `json:"valid_from,omitempty"`
	ValidUntil   *string        `json:"valid_until,omitempty"`
	ReviewStatus string         `json:"review_status,omitempty"`
}

const memoryFactSelect = "id,user_id,fact,entity_name,topic,source_id,supersedes_id,active,created_at,content_version,memory_kind,predicate,object_value,confidence,sensitivity,valid_from,valid_until,review_status"

// MemoryFactInput is used to insert a structured V2 memory.
type MemoryFactInput struct {
	Fact           string
	EntityName     *string
	Topic          *string
	SourceID       *string
	SupersedesID   *string
	MemoryKind     string
	Predicate      string
	ObjectValue    map[string]any
	Confidence     float64
	Sensitivity    string
	ValidFrom      *time.Time
	ValidUntil     *time.Time
	ReviewStatus   string
	ContentVersion int64
}

// MemoryEvidence links a fact to a provenance excerpt.
type MemoryEvidence struct {
	ID         string         `json:"id"`
	UserID     string         `json:"user_id"`
	FactID     string         `json:"fact_id"`
	SourceKind string         `json:"source_kind"`
	SourceID   *string        `json:"source_id,omitempty"`
	Excerpt    string         `json:"excerpt"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  string         `json:"created_at,omitempty"`
}

// MemoryRetrievalEvent stores retrieval telemetry (plan + summary).
type MemoryRetrievalEvent struct {
	ID            string         `json:"id"`
	UserID        string         `json:"user_id"`
	SessionID     *string        `json:"session_id,omitempty"`
	QueryText     string         `json:"query_text"`
	Plan          map[string]any `json:"plan"`
	ResultSummary map[string]any `json:"result_summary"`
	LatencyMs     *int           `json:"latency_ms,omitempty"`
	CreatedAt     string         `json:"created_at,omitempty"`
}

// InsertMemoryFact writes a structured V2 fact (active or pending_review).
func (k *Knowledge) InsertMemoryFact(ctx context.Context, userID string, in MemoryFactInput) (MemoryFact, error) {
	if !k.Enabled {
		return MemoryFact{}, fmt.Errorf("knowledge disabled")
	}
	kind := strings.TrimSpace(in.MemoryKind)
	if kind == "" {
		kind = MemoryKindFact
	}
	sens := strings.TrimSpace(in.Sensitivity)
	if sens == "" {
		sens = SensitivityNormal
	}
	status := strings.TrimSpace(in.ReviewStatus)
	if status == "" {
		status = ReviewActive
	}
	conf := in.Confidence
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	active := status == ReviewActive

	row := map[string]any{
		"user_id":       userID,
		"fact":          strings.TrimSpace(in.Fact),
		"entity_name":   in.EntityName,
		"topic":         in.Topic,
		"source_id":     in.SourceID,
		"supersedes_id": in.SupersedesID,
		"active":        active,
		"memory_kind":   kind,
		"predicate":     strings.TrimSpace(in.Predicate),
		"confidence":    conf,
		"sensitivity":   sens,
		"review_status": status,
	}
	if in.ObjectValue != nil {
		row["object_value"] = in.ObjectValue
	}
	if in.ContentVersion > 0 {
		row["content_version"] = in.ContentVersion
	}
	if in.ValidFrom != nil {
		row["valid_from"] = in.ValidFrom.UTC().Format(time.RFC3339Nano)
	}
	if in.ValidUntil != nil {
		row["valid_until"] = in.ValidUntil.UTC().Format(time.RFC3339Nano)
	}

	if k.Embedder != nil && k.Embedder.Enabled() && active {
		embInput := factEmbeddingInput(NewFactInput{
			Fact:       in.Fact,
			EntityName: in.EntityName,
			Topic:      in.Topic,
		})
		if vec, err := k.Embedder.EmbedOne(ctx, embInput); err == nil {
			row["embedding"] = vec
		} else {
			log.Warn("insert memory fact: embedding failed", map[string]any{
				"userId": log.ShortID(userID),
				"error":  err.Error(),
			})
		}
	}

	var dest []MemoryFact
	if err := k.DB.Insert(ctx, "kb_facts", row, &dest); err != nil {
		return MemoryFact{}, err
	}
	if len(dest) == 0 {
		return MemoryFact{}, fmt.Errorf("failed to insert memory fact")
	}
	return dest[0], nil
}

// ListActiveMemoryFacts returns confirmed (active + review_status=active) V2/legacy facts.
func (k *Knowledge) ListActiveMemoryFacts(ctx context.Context, userID string, limit int) ([]MemoryFact, error) {
	if !k.Enabled {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	q := url.Values{}
	q.Set("select", memoryFactSelect)
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")
	q.Set("or", "(review_status.eq.active,review_status.is.null)")
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []MemoryFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ListActiveMemoryFactsByKinds filters confirmed facts by memory_kind.
func (k *Knowledge) ListActiveMemoryFactsByKinds(ctx context.Context, userID string, kinds []string, limit int) ([]MemoryFact, error) {
	if !k.Enabled || len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	cleaned := make([]string, 0, len(kinds))
	for _, knd := range kinds {
		knd = strings.TrimSpace(knd)
		if knd != "" {
			cleaned = append(cleaned, knd)
		}
	}
	if len(cleaned) == 0 {
		return nil, nil
	}
	q := url.Values{}
	q.Set("select", memoryFactSelect)
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")
	q.Set("or", "(review_status.eq.active,review_status.is.null)")
	q.Set("memory_kind", "in.("+strings.Join(cleaned, ",")+")")
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []MemoryFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// SearchMemoryFactsLexical runs FTS over confirmed facts (no embeddings).
func (k *Knowledge) SearchMemoryFactsLexical(ctx context.Context, userID, query string, limit int) ([]MemoryFact, error) {
	if !k.Enabled {
		return nil, nil
	}
	terms := extractSearchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	tsQuery := strings.Join(terms, " | ")
	q := url.Values{}
	q.Set("select", memoryFactSelect)
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")
	q.Set("or", "(review_status.eq.active,review_status.is.null)")
	q.Set("search_vector", "fts.english.websearch."+tsQuery)
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []MemoryFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// MarkMemoryFactSuperseded deactivates a fact and marks review_status=superseded.
func (k *Knowledge) MarkMemoryFactSuperseded(ctx context.Context, userID, factID string) error {
	if !k.Enabled {
		return fmt.Errorf("knowledge disabled")
	}
	q := url.Values{}
	q.Set("id", "eq."+factID)
	q.Set("user_id", "eq."+userID)
	return k.DB.Patch(ctx, "kb_facts", q, map[string]any{
		"active":        false,
		"review_status": ReviewSuperseded,
	})
}

// ReinforceMemoryFact bumps confidence (capped at 1) and content_version.
func (k *Knowledge) ReinforceMemoryFact(ctx context.Context, userID, factID string, confidence float64) error {
	if !k.Enabled {
		return fmt.Errorf("knowledge disabled")
	}
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	q := url.Values{}
	q.Set("id", "eq."+factID)
	q.Set("user_id", "eq."+userID)
	return k.DB.Patch(ctx, "kb_facts", q, map[string]any{
		"confidence": confidence,
	})
}

// InsertMemoryEvidence stores a provenance row for a fact.
func (k *Knowledge) InsertMemoryEvidence(ctx context.Context, userID, factID, sourceKind string, sourceID *string, excerpt string, metadata map[string]any) (MemoryEvidence, error) {
	if !k.Enabled {
		return MemoryEvidence{}, fmt.Errorf("knowledge disabled")
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	body := map[string]any{
		"user_id":     userID,
		"fact_id":     factID,
		"source_kind": sourceKind,
		"excerpt":     excerpt,
		"metadata":    metadata,
	}
	if sourceID != nil && strings.TrimSpace(*sourceID) != "" {
		body["source_id"] = strings.TrimSpace(*sourceID)
	}
	var dest []MemoryEvidence
	if err := k.DB.Insert(ctx, "kb_memory_evidence", body, &dest); err != nil {
		return MemoryEvidence{}, err
	}
	if len(dest) == 0 {
		return MemoryEvidence{}, fmt.Errorf("failed to insert memory evidence")
	}
	return dest[0], nil
}

// InsertMemorySuggestion stores a pending memory review suggestion.
func (k *Knowledge) InsertMemorySuggestion(ctx context.Context, userID string, payload map[string]any, confidence float64, targetFactID *string) (MemorySuggestion, error) {
	if !k.Enabled {
		return MemorySuggestion{}, fmt.Errorf("knowledge disabled")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	body := map[string]any{
		"user_id":         userID,
		"suggestion_kind": SuggestionKindMemory,
		"status":          SuggestionPending,
		"payload":         payload,
		"confidence":      confidence,
	}
	if targetFactID != nil && strings.TrimSpace(*targetFactID) != "" {
		body["target_fact_id"] = strings.TrimSpace(*targetFactID)
	}
	var dest []MemorySuggestion
	if err := k.DB.Insert(ctx, "memory_suggestions", body, &dest); err != nil {
		return MemorySuggestion{}, err
	}
	if len(dest) == 0 {
		return MemorySuggestion{}, fmt.Errorf("failed to insert memory suggestion")
	}
	return dest[0], nil
}

// InsertMemoryRetrievalEvent records retrieval plan/result telemetry.
func (k *Knowledge) InsertMemoryRetrievalEvent(ctx context.Context, userID, sessionID, query string, plan, summary map[string]any, latencyMs int) error {
	if !k.Enabled {
		return nil
	}
	if plan == nil {
		plan = map[string]any{}
	}
	if summary == nil {
		summary = map[string]any{}
	}
	body := map[string]any{
		"user_id":        userID,
		"query_text":     query,
		"plan":           plan,
		"result_summary": summary,
		"latency_ms":     latencyMs,
	}
	if strings.TrimSpace(sessionID) != "" {
		body["session_id"] = sessionID
	}
	var dest []MemoryRetrievalEvent
	return k.DB.Insert(ctx, "memory_retrieval_events", body, &dest)
}

// ProjectProfileSummary builds a profile summary from confirmed structured memories.
// Identity and preference facts are preferred; the result is not an opaque LLM overwrite.
func ProjectProfileSummary(facts []MemoryFact) string {
	var identity, prefs, other []string
	for _, f := range facts {
		if !f.Active {
			continue
		}
		if f.ReviewStatus != "" && f.ReviewStatus != ReviewActive {
			continue
		}
		text := strings.TrimSpace(f.Fact)
		if text == "" {
			continue
		}
		kind := ""
		if f.MemoryKind != nil {
			kind = *f.MemoryKind
		}
		switch kind {
		case MemoryKindIdentity:
			identity = append(identity, text)
		case MemoryKindPreference:
			prefs = append(prefs, text)
		default:
			if kind == MemoryKindRelationship || kind == MemoryKindGoal || kind == MemoryKindProject {
				other = append(other, text)
			}
		}
	}
	parts := make([]string, 0, len(identity)+len(prefs)+3)
	parts = append(parts, identity...)
	parts = append(parts, prefs...)
	// Keep projection compact: at most 3 additional durable facts.
	if len(other) > 3 {
		other = other[:3]
	}
	parts = append(parts, other...)
	return strings.Join(parts, " ")
}

// UpsertProjectedProfileSummary writes the projection of confirmed memories.
func (k *Knowledge) UpsertProjectedProfileSummary(ctx context.Context, userID string) error {
	facts, err := k.ListActiveMemoryFacts(ctx, userID, 100)
	if err != nil {
		return err
	}
	summary := ProjectProfileSummary(facts)
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	return k.UpsertUserProfileSummary(ctx, userID, summary)
}
