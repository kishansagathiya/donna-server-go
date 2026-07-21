package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	extractAutoMin     = 0.90
	extractEnricherVer = 1
)

// Extractor handles JobTypeMemoryExtract background jobs.
type Extractor struct {
	KB    *storage.Knowledge
	Notes *storage.Notes
	LLM   *providers.LLM
	Flags *featureflags.Resolver
}

type extractPayload struct {
	SourceKind     string `json:"source_kind"`
	SourceID       string `json:"source_id"`
	Content        string `json:"content"`
	Excerpt        string `json:"excerpt"`
	ConversationID string `json:"conversation_id"`
	TurnIndex      int    `json:"turn_index"`
	NoteID         string `json:"note_id"`
}

type reconcileAction string

const (
	actionAdd       reconcileAction = "add"
	actionReinforce reconcileAction = "reinforce"
	actionSupersede reconcileAction = "supersede"
	actionReview    reconcileAction = "review"
	actionIgnore    reconcileAction = "ignore"
)

// HandleJob is a jobs.Handler for JobTypeMemoryExtract.
func (e *Extractor) HandleJob(ctx context.Context, job storage.BackgroundJob) error {
	if e == nil || e.KB == nil || !e.KB.Enabled {
		return nil
	}
	userID := ""
	if job.UserID != nil {
		userID = strings.TrimSpace(*job.UserID)
	}
	if userID == "" {
		return nil
	}

	if e.Flags != nil {
		flags, err := e.Flags.NotesMemoryV2ForUser(ctx, userID)
		if err != nil {
			return err
		}
		if !flags.MemoryExtraction {
			return nil
		}
	}

	payload, err := parseExtractPayload(job)
	if err != nil {
		return err
	}
	content := strings.TrimSpace(payload.Content)
	if content == "" {
		content = strings.TrimSpace(payload.Excerpt)
	}
	if content == "" {
		return nil
	}

	existing, err := e.KB.ListActiveMemoryFacts(ctx, userID, 300)
	if err != nil {
		return err
	}

	candidates, err := e.extractCandidates(ctx, content, existing)
	if err != nil {
		return err
	}

	activated := 0
	reviewed := 0
	for _, c := range candidates {
		if reason := RejectUnsafe(c); reason != RejectNone {
			log.Print("memory extract rejected unsafe", map[string]any{
				"userId": log.ShortID(userID),
				"reason": string(reason),
			})
			continue
		}
		if reason := ShouldDiscard(c); reason != RejectNone {
			continue
		}

		action, match := reconcile(c, existing)
		conflicting := action == actionReview && match != nil && valuesConflict(c, *match)

		switch {
		case action == actionIgnore:
			continue
		case action == actionReinforce && match != nil && CanAutoActivate(c, false):
			newConf := c.Confidence
			if match.Confidence != nil && *match.Confidence > newConf {
				newConf = *match.Confidence
			}
			if newConf < extractAutoMin {
				newConf = extractAutoMin
			}
			if err := e.KB.ReinforceMemoryFact(ctx, userID, match.ID, newConf); err != nil {
				return err
			}
			if err := e.writeEvidence(ctx, userID, match.ID, payload); err != nil {
				log.Warn("memory evidence write failed", map[string]any{"error": err.Error()})
			}
			activated++
		case CanAutoActivate(c, conflicting) && (action == actionAdd || action == actionSupersede):
			fact, err := e.activate(ctx, userID, c, payload, match, action)
			if err != nil {
				return err
			}
			existing = append([]storage.MemoryFact{fact}, existing...)
			activated++
		case NeedsReview(c, conflicting) || action == actionReview:
			if err := e.queueReview(ctx, userID, c, payload, match, conflicting); err != nil {
				return err
			}
			reviewed++
		default:
			// ignore
		}
	}

	if activated > 0 {
		if err := e.KB.UpsertProjectedProfileSummary(ctx, userID); err != nil {
			log.Warn("profile projection failed", map[string]any{
				"userId": log.ShortID(userID),
				"error":  err.Error(),
			})
		}
	}

	log.Print("memory extract completed", map[string]any{
		"userId":     log.ShortID(userID),
		"activated":  activated,
		"reviewed":   reviewed,
		"candidates": len(candidates),
	})
	return nil
}

func parseExtractPayload(job storage.BackgroundJob) (extractPayload, error) {
	var p extractPayload
	if len(job.Payload) == 0 {
		return p, fmt.Errorf("memory_extract: empty payload")
	}
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return p, err
	}
	return p, nil
}

func (e *Extractor) extractCandidates(ctx context.Context, content string, existing []storage.MemoryFact) ([]Candidate, error) {
	if e.LLM == nil {
		return nil, nil
	}
	system := strings.Join([]string{
		fmt.Sprintf("You are Donna's memory extractor v%d.", extractEnricherVer),
		"Extract durable, atomic personal memories from NEW user content only.",
		"Return ONLY strict JSON: {\"memories\":[{...}]}",
		"Each memory: kind (identity|preference|relationship|goal|project|habit|location|event|fact|other),",
		"predicate (snake_case), value (object), fact (natural sentence), entity_name,",
		"confidence (0..1), sensitivity (normal|sensitive|restricted), explicit (bool), ephemeral (bool),",
		"valid_from, valid_until (ISO8601 or null).",
		"Do NOT extract credentials, secrets, passwords, API keys, or payment details.",
		"Do NOT infer protected traits (race, religion, politics, health, orientation, etc).",
		"Mark ephemeral=true for temporary logistics (today's weather, one-off meeting times).",
		"explicit=true only when the user clearly stated the fact about themselves or asked to remember it.",
		"Prefer at most 8 memories. Skip fluff.",
	}, "\n")

	var b strings.Builder
	b.WriteString("Existing memories (for context; do not re-extract):\n")
	limit := len(existing)
	if limit > 40 {
		limit = 40
	}
	for i := 0; i < limit; i++ {
		f := existing[i]
		b.WriteString("- ")
		b.WriteString(f.Fact)
		b.WriteString("\n")
	}
	b.WriteString("\nNew content:\n")
	b.WriteString(content)

	raw, err := e.LLM.CompleteOnce(ctx, []providers.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: b.String()},
	})
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(raw)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("memory_extract_invalid_llm_json")
	}
	var parsed struct {
		Memories []Candidate `json:"memories"`
	}
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &parsed); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(parsed.Memories))
	for _, c := range parsed.Memories {
		c.Kind = normalizeKind(c.Kind)
		c.Predicate = strings.ToLower(strings.TrimSpace(c.Predicate))
		c.Fact = strings.TrimSpace(c.Fact)
		c.EntityName = strings.TrimSpace(c.EntityName)
		c.Sensitivity = normalizeSensitivity(c.Sensitivity)
		if c.Confidence < 0 {
			c.Confidence = 0
		}
		if c.Confidence > 1 {
			c.Confidence = 1
		}
		if c.Fact == "" && c.Predicate != "" {
			c.Fact = renderFact(c)
		}
		if c.Fact == "" {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func normalizeKind(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case storage.MemoryKindIdentity, storage.MemoryKindPreference, storage.MemoryKindRelationship,
		storage.MemoryKindGoal, storage.MemoryKindProject, storage.MemoryKindHabit, storage.MemoryKindRoutine,
		storage.MemoryKindLocation, storage.MemoryKindEvent, storage.MemoryKindFact, storage.MemoryKindOther,
		storage.MemoryKindConstraint, storage.MemoryKindInstruction:
		return strings.ToLower(strings.TrimSpace(k))
	default:
		return storage.MemoryKindFact
	}
}

func normalizeSensitivity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case storage.SensitivitySensitive:
		return storage.SensitivitySensitive
	case storage.SensitivityRestricted:
		return storage.SensitivityRestricted
	default:
		return storage.SensitivityNormal
	}
}

func renderFact(c Candidate) string {
	val := strings.TrimSpace(valueBlob(c.Value))
	if c.EntityName != "" && c.Predicate != "" && val != "" {
		return fmt.Sprintf("%s %s %s", c.EntityName, strings.ReplaceAll(c.Predicate, "_", " "), val)
	}
	if c.Predicate != "" && val != "" {
		return c.Predicate + ": " + val
	}
	return val
}

func reconcile(c Candidate, existing []storage.MemoryFact) (reconcileAction, *storage.MemoryFact) {
	pred := strings.ToLower(strings.TrimSpace(c.Predicate))
	entity := strings.ToLower(strings.TrimSpace(c.EntityName))
	factLower := strings.ToLower(strings.TrimSpace(c.Fact))

	var best *storage.MemoryFact
	for i := range existing {
		f := &existing[i]
		fPred := ""
		if f.Predicate != nil {
			fPred = strings.ToLower(strings.TrimSpace(*f.Predicate))
		}
		fEntity := ""
		if f.EntityName != nil {
			fEntity = strings.ToLower(strings.TrimSpace(*f.EntityName))
		}
		sameSlot := pred != "" && fPred == pred && (entity == "" || fEntity == "" || fEntity == entity)
		sameText := factLower != "" && strings.EqualFold(strings.TrimSpace(f.Fact), c.Fact)
		nearText := factLower != "" && (strings.Contains(strings.ToLower(f.Fact), factLower) || strings.Contains(factLower, strings.ToLower(f.Fact)))
		if sameText || (sameSlot && nearText) {
			return actionReinforce, f
		}
		if sameSlot {
			if valuesConflict(c, *f) {
				return actionReview, f
			}
			// Same predicate/entity with different wording → supersede when explicit high confidence.
			best = f
		}
	}
	if best != nil {
		return actionSupersede, best
	}
	return actionAdd, nil
}

func valuesConflict(c Candidate, f storage.MemoryFact) bool {
	cand := strings.ToLower(strings.TrimSpace(valueBlob(c.Value)))
	if cand == "" {
		cand = strings.ToLower(strings.TrimSpace(c.Fact))
	}
	existing := strings.ToLower(strings.TrimSpace(f.Fact))
	if f.ObjectValue != nil {
		if v, ok := f.ObjectValue["text"].(string); ok && strings.TrimSpace(v) != "" {
			existing = strings.ToLower(strings.TrimSpace(v))
		} else if v, ok := f.ObjectValue["value"].(string); ok && strings.TrimSpace(v) != "" {
			existing = strings.ToLower(strings.TrimSpace(v))
		}
	}
	if cand == "" || existing == "" {
		return false
	}
	if cand == existing || strings.Contains(existing, cand) || strings.Contains(cand, existing) {
		return false
	}
	// Same slot, non-overlapping values → conflict.
	return true
}

func (e *Extractor) activate(
	ctx context.Context,
	userID string,
	c Candidate,
	payload extractPayload,
	match *storage.MemoryFact,
	action reconcileAction,
) (storage.MemoryFact, error) {
	in := storage.MemoryFactInput{
		Fact:         c.Fact,
		MemoryKind:   c.Kind,
		Predicate:    c.Predicate,
		ObjectValue:  c.Value,
		Confidence:   c.Confidence,
		Sensitivity:  c.Sensitivity,
		ReviewStatus: storage.ReviewActive,
	}
	if c.EntityName != "" {
		name := c.EntityName
		in.EntityName = &name
	}
	topic := c.Kind
	in.Topic = &topic
	if payload.SourceID != "" {
		sid := payload.SourceID
		in.SourceID = &sid
	}
	if vf := parseOptionalTime(c.ValidFrom); vf != nil {
		in.ValidFrom = vf
	}
	if vu := parseOptionalTime(c.ValidUntil); vu != nil {
		in.ValidUntil = vu
	}
	if action == actionSupersede && match != nil {
		if err := e.KB.MarkMemoryFactSuperseded(ctx, userID, match.ID); err != nil {
			return storage.MemoryFact{}, err
		}
		sid := match.ID
		in.SupersedesID = &sid
	}
	fact, err := e.KB.InsertMemoryFact(ctx, userID, in)
	if err != nil {
		return storage.MemoryFact{}, err
	}
	if err := e.writeEvidence(ctx, userID, fact.ID, payload); err != nil {
		log.Warn("memory evidence write failed", map[string]any{"error": err.Error()})
	}
	return fact, nil
}

func (e *Extractor) queueReview(
	ctx context.Context,
	userID string,
	c Candidate,
	payload extractPayload,
	match *storage.MemoryFact,
	conflicting bool,
) error {
	payloadMap := map[string]any{
		"kind":         c.Kind,
		"predicate":    c.Predicate,
		"value":        c.Value,
		"fact":         c.Fact,
		"entity_name":  c.EntityName,
		"sensitivity":  c.Sensitivity,
		"explicit":     c.Explicit,
		"source_kind":  payload.SourceKind,
		"source_id":    payload.SourceID,
		"excerpt":      firstNonEmpty(payload.Excerpt, truncate(payload.Content, 400)),
		"conflicting":  conflicting,
		"confidence":   c.Confidence,
	}
	if match != nil {
		payloadMap["conflict_with_fact_id"] = match.ID
		payloadMap["conflict_with_fact"] = match.Fact
	}
	var targetFactID *string
	if match != nil {
		targetFactID = &match.ID
	}
	_, err := e.KB.InsertMemorySuggestion(ctx, userID, payloadMap, c.Confidence, targetFactID)
	return err
}

func (e *Extractor) writeEvidence(ctx context.Context, userID, factID string, payload extractPayload) error {
	kind := strings.TrimSpace(payload.SourceKind)
	if kind == "" {
		kind = storage.EvidenceManual
	}
	var sourceID *string
	if payload.SourceID != "" {
		sid := payload.SourceID
		sourceID = &sid
	} else if payload.NoteID != "" {
		sid := payload.NoteID
		sourceID = &sid
		if kind == storage.EvidenceManual {
			kind = storage.EvidenceNote
		}
	}
	excerpt := firstNonEmpty(payload.Excerpt, truncate(payload.Content, 500))
	meta := map[string]any{}
	if payload.ConversationID != "" {
		meta["conversation_id"] = payload.ConversationID
		meta["turn_index"] = payload.TurnIndex
	}
	if payload.NoteID != "" {
		meta["note_id"] = payload.NoteID
	}
	_, err := e.KB.InsertMemoryEvidence(ctx, userID, factID, kind, sourceID, excerpt, meta)
	return err
}

func parseOptionalTime(raw *string) *time.Time {
	if raw == nil {
		return nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" || strings.EqualFold(s, "null") {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}