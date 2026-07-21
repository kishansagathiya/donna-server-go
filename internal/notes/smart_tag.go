package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	smartTagAutoMin     = 0.85
	smartTagSuggestMin  = 0.55
	smartTagEnricherVer = 1
)

// SmartTagEnricher proposes and applies personal smart tags for a note.
type SmartTagEnricher struct {
	Store *storage.Notes
	LLM   *providers.LLM
	Flags *featureflags.Resolver
}

type tagProposal struct {
	Tag        string  `json:"tag"`
	Confidence float64 `json:"confidence"`
}

// HandleJob is a jobs.Handler for JobTypeSmartTagEnrich.
func (e *SmartTagEnricher) HandleJob(ctx context.Context, job storage.BackgroundJob) error {
	if e == nil || e.Store == nil || !e.Store.Enabled {
		return nil
	}
	userID := ""
	if job.UserID != nil {
		userID = strings.TrimSpace(*job.UserID)
	}
	noteID := ""
	if job.TargetID != nil {
		noteID = strings.TrimSpace(*job.TargetID)
	}
	if noteID == "" && len(job.Payload) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(job.Payload, &payload); err == nil {
			if raw, ok := payload["note_id"].(string); ok {
				noteID = strings.TrimSpace(raw)
			}
		}
	}
	if noteID == "" || userID == "" {
		return nil
	}

	if e.Flags != nil {
		flags, err := e.Flags.NotesMemoryV2ForUser(ctx, userID)
		if err != nil {
			return err
		}
		if !flags.SmartTagging {
			return nil
		}
	}

	note, err := e.Store.GetNoteByID(ctx, userID, noteID)
	if err != nil {
		return err
	}
	if job.TargetVersion != nil && note.ContentVersion > *job.TargetVersion {
		// Extra guard; worker already no-ops stale jobs.
		return nil
	}

	_ = e.Store.MarkEnrichmentRunning(ctx, userID, noteID)

	proposals, err := e.propose(ctx, userID, note)
	if err != nil {
		_ = e.Store.MarkEnrichmentFailed(ctx, userID, noteID)
		return err
	}

	auto := make([]string, 0, len(proposals))
	for _, p := range proposals {
		canonical, resolveErr := e.Store.ResolveCanonical(ctx, userID, p.Tag)
		if resolveErr != nil {
			_ = e.Store.MarkEnrichmentFailed(ctx, userID, noteID)
			return resolveErr
		}
		if canonical == "" {
			continue
		}
		switch {
		case p.Confidence >= smartTagAutoMin:
			auto = append(auto, canonical)
		case p.Confidence >= smartTagSuggestMin:
			if err := e.Store.InsertTagSuggestion(ctx, userID, noteID, canonical, p.Confidence); err != nil {
				log.Warn("tag suggestion insert failed", map[string]any{
					"noteId": log.ShortID(noteID),
					"error":  err.Error(),
				})
			}
		default:
			// discard
		}
	}

	if len(auto) > 0 {
		if _, err := e.Store.ApplyAutoTags(ctx, userID, noteID, auto); err != nil {
			_ = e.Store.MarkEnrichmentFailed(ctx, userID, noteID)
			return err
		}
	}

	if err := e.Store.MarkEnrichmentSucceeded(ctx, userID, noteID, note.ContentVersion); err != nil {
		return err
	}
	return nil
}

func (e *SmartTagEnricher) propose(ctx context.Context, userID string, note storage.Note) ([]tagProposal, error) {
	taxonomy, err := e.Store.ListTaxonomy(ctx, userID, 80)
	if err != nil {
		return nil, err
	}
	corrections, err := e.Store.ListRecentTagCorrections(ctx, userID, 15)
	if err != nil {
		return nil, err
	}

	if e.LLM != nil {
		if proposals, llmErr := e.proposeWithLLM(ctx, note.Content, taxonomy, corrections); llmErr == nil && len(proposals) > 0 {
			return proposals, nil
		} else if llmErr != nil {
			log.Warn("smart tag LLM propose failed; falling back", map[string]any{
				"noteId": log.ShortID(note.ID),
				"error":  llmErr.Error(),
			})
		}
	}
	return e.proposeLexical(note.Content, taxonomy), nil
}

func (e *SmartTagEnricher) proposeWithLLM(
	ctx context.Context,
	content string,
	taxonomy []storage.TaxonomyTag,
	corrections []storage.MemoryFeedback,
) ([]tagProposal, error) {
	taxNames := make([]string, 0, len(taxonomy))
	for _, t := range taxonomy {
		if t.AliasOf != nil && strings.TrimSpace(*t.AliasOf) != "" {
			continue
		}
		taxNames = append(taxNames, t.Name)
	}
	corrLines := make([]string, 0, len(corrections))
	for _, c := range corrections {
		b, _ := json.Marshal(c.Details)
		corrLines = append(corrLines, string(b))
	}

	system := strings.Join([]string{
		fmt.Sprintf("You are Donna's personal smart-tag enricher v%d.", smartTagEnricherVer),
		"Propose tags for a personal note using the user's existing taxonomy when possible.",
		"Return ONLY strict JSON: {\"tags\":[{\"tag\":\"...\",\"confidence\":0.0}]}",
		"confidence must be 0..1. Prefer existing tag names. Suggest at most 5 tags.",
		"Do not invent sensitive/inferred protected traits.",
	}, "\n")

	user := strings.Builder{}
	user.WriteString("Taxonomy:\n")
	if len(taxNames) == 0 {
		user.WriteString("(empty)\n")
	} else {
		user.WriteString(strings.Join(taxNames, ", "))
		user.WriteString("\n")
	}
	if len(corrLines) > 0 {
		user.WriteString("Recent corrections:\n")
		user.WriteString(strings.Join(corrLines, "\n"))
		user.WriteString("\n")
	}
	user.WriteString("Note:\n")
	user.WriteString(content)

	raw, err := e.LLM.CompleteOnce(ctx, []providers.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user.String()},
	})
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(raw)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("smart_tag_invalid_llm_json")
	}
	var parsed struct {
		Tags []tagProposal `json:"tags"`
	}
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &parsed); err != nil {
		return nil, err
	}
	out := make([]tagProposal, 0, len(parsed.Tags))
	for _, t := range parsed.Tags {
		tag := storage.NormalizeTagUnicode(t.Tag)
		if tag == "" {
			continue
		}
		conf := t.Confidence
		if conf < 0 {
			conf = 0
		}
		if conf > 1 {
			conf = 1
		}
		out = append(out, tagProposal{Tag: tag, Confidence: conf})
	}
	return out, nil
}

func (e *SmartTagEnricher) proposeLexical(content string, taxonomy []storage.TaxonomyTag) []tagProposal {
	lower := strings.ToLower(content)
	out := make([]tagProposal, 0)
	for _, t := range taxonomy {
		if t.AliasOf != nil && strings.TrimSpace(*t.AliasOf) != "" {
			continue
		}
		name := storage.NormalizeTagUnicode(t.Name)
		if name == "" || !strings.Contains(lower, name) {
			continue
		}
		conf := 0.72
		if t.Pinned {
			conf = 0.9
		} else if t.Count >= 3 {
			conf = 0.86
		}
		out = append(out, tagProposal{Tag: name, Confidence: conf})
		if len(out) >= 5 {
			break
		}
	}
	return out
}
