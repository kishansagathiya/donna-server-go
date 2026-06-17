package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/notes"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

var errSourceNotOwned = errors.New("source does not belong to user")

type Compiler struct {
	KB    *storage.Knowledge
	LLM   *providers.LLM
	Notes *notes.Sync
}

type compilerOutput struct {
	ProfileSummary *string `json:"profile_summary"`
	NewFacts       []struct {
		Fact              string  `json:"fact"`
		EntityName        *string `json:"entity_name"`
		Topic             *string `json:"topic"`
		SourceTurnIndex   *int    `json:"source_turn_index"`
	} `json:"new_facts"`
	Supersede []struct {
		OldFact    string  `json:"old_fact"`
		NewFact    string  `json:"new_fact"`
		EntityName *string `json:"entity_name"`
		Topic      *string `json:"topic"`
	} `json:"supersede"`
}

func (c *Compiler) CompileSource(ctx context.Context, userID, sourceID string) error {
	compiled, err := c.KB.IsSourceCompiled(ctx, sourceID)
	if err != nil {
		return err
	}
	if compiled {
		c.KB.LogKnowledge("asset compile skipped — already completed", map[string]any{"sourceId": sourceID})
		return nil
	}

	source, err := c.KB.GetSourceByID(ctx, sourceID)
	if err != nil {
		return err
	}
	if source.UserID != userID {
		return errSourceNotOwned
	}

	existingProfile, _ := c.KB.GetUserProfileSummary(ctx, userID)
	existingFacts, err := c.KB.GetActiveFacts(ctx, userID)
	if err != nil {
		return err
	}

	label := "Asset"
	if source.Metadata != nil {
		if t, ok := source.Metadata["title"].(string); ok && t != "" {
			label = t
		} else if k, ok := source.Metadata["asset_kind"].(string); ok && k != "" {
			label = k
		}
	}

	output, err := c.runCompilerLLM(ctx, existingProfile, existingFacts, []CompilerSource{{
		ID:      &source.ID,
		Label:   label,
		Content: source.Content,
	}})
	if err != nil {
		return err
	}

	obviousSources := []SourceSlice{{ID: &source.ID, Content: source.Content}}
	factsAdded, profileUpdated, err := c.applyCompilerOutput(ctx, userID, sourceID, existingFacts, output, nil, obviousSources)
	if err != nil {
		return err
	}

	if factsAdded > 0 || profileUpdated {
		if err := c.KB.MarkSourceCompiled(ctx, sourceID); err != nil {
			return err
		}
	}

	c.KB.LogKnowledge("asset compiled", map[string]any{
		"userId":    shortID(userID),
		"sourceId":  sourceID,
		"factsAdded": factsAdded,
	})
	return nil
}

func (c *Compiler) CompileConversation(ctx context.Context, userID, conversationID string) error {
	compiled, err := c.KB.IsConversationCompiled(ctx, conversationID)
	if err != nil {
		return err
	}
	if compiled {
		c.KB.LogKnowledge("compile skipped — already completed", map[string]any{"conversationId": conversationID})
		return nil
	}

	logID, err := c.KB.CreateCompileLog(ctx, userID, conversationID)
	if err != nil {
		return err
	}

	_, err = c.KB.WaitForConversationTurns(ctx, conversationID)
	if err != nil {
		c.KB.CompleteCompileLog(ctx, logID, "failed", 0, 0, err.Error())
		return err
	}

	sources, err := c.KB.SyncConversationSources(ctx, userID, conversationID)
	if err != nil {
		c.KB.CompleteCompileLog(ctx, logID, "failed", 0, 0, err.Error())
		return err
	}
	if c.Notes != nil {
		_ = c.Notes.FromVoiceSources(ctx, userID, sources)
	}
	if len(sources) == 0 {
		c.KB.CompleteCompileLog(ctx, logID, "failed", 0, 0, "no conversation turns to compile")
		c.KB.LogKnowledge("compile skipped — no turns found", map[string]any{"conversationId": conversationID})
		return nil
	}

	existingProfile, _ := c.KB.GetUserProfileSummary(ctx, userID)
	existingFacts, err := c.KB.GetActiveFacts(ctx, userID)
	if err != nil {
		c.KB.CompleteCompileLog(ctx, logID, "failed", 0, 0, err.Error())
		return err
	}

	compilerSources := make([]CompilerSource, 0, len(sources))
	sourceByTurn := make(map[int]string)
	for _, s := range sources {
		label := ""
		if s.TurnIndex != nil {
			label = fmt.Sprintf("Turn %d", *s.TurnIndex)
			sourceByTurn[*s.TurnIndex] = s.ID
		}
		compilerSources = append(compilerSources, CompilerSource{
			ID:        &s.ID,
			TurnIndex: s.TurnIndex,
			Label:     label,
			Content:   s.Content,
		})
	}

	output, err := c.runCompilerLLM(ctx, existingProfile, existingFacts, compilerSources)
	if err != nil {
		c.KB.CompleteCompileLog(ctx, logID, "failed", 0, 0, err.Error())
		return err
	}

	obviousSources := make([]SourceSlice, 0, len(sources))
	for _, s := range sources {
		id := s.ID
		obviousSources = append(obviousSources, SourceSlice{ID: &id, Content: s.Content, TurnIndex: s.TurnIndex})
	}
	factsAdded, _, err := c.applyCompilerOutput(ctx, userID, "", existingFacts, output, sourceByTurn, obviousSources)
	if err != nil {
		c.KB.CompleteCompileLog(ctx, logID, "failed", 0, 0, err.Error())
		return err
	}

	c.KB.CompleteCompileLog(ctx, logID, "completed", len(sources), factsAdded, "")
	c.KB.LogKnowledge("knowledge compiled", map[string]any{
		"userId":         shortID(userID),
		"conversationId": conversationID,
		"turns":          len(sources),
		"factsAdded":     factsAdded,
	})
	return nil
}

func (c *Compiler) runCompilerLLM(ctx context.Context, existingProfile string, existingFacts []storage.KbFact, sources []CompilerSource) (compilerOutput, error) {
	existing := make([]CompilerExistingFact, 0, len(existingFacts))
	for _, f := range existingFacts {
		existing = append(existing, CompilerExistingFact{
			ID:         f.ID,
			Fact:       f.Fact,
			EntityName: f.EntityName,
		})
	}

	messages := []providers.ChatMessage{
		{Role: "system", Content: KBCompilerSystemPrompt},
		{Role: "user", Content: BuildCompilerUserMessage(existingProfile, existing, sources)},
	}

	raw, err := c.LLM.CompleteOnce(ctx, messages)
	if err != nil {
		return compilerOutput{}, err
	}
	return parseCompilerOutput(raw), nil
}

func (c *Compiler) applyCompilerOutput(
	ctx context.Context,
	userID, defaultSourceID string,
	existingFacts []storage.KbFact,
	output compilerOutput,
	sourceByTurn map[int]string,
	obviousSources []SourceSlice,
) (factsAdded int, profileUpdated bool, err error) {
	if output.ProfileSummary != nil && strings.TrimSpace(*output.ProfileSummary) != "" {
		if err := c.KB.UpsertUserProfileSummary(ctx, userID, strings.TrimSpace(*output.ProfileSummary)); err != nil {
			return 0, false, err
		}
		profileUpdated = true
	}

	for _, item := range output.Supersede {
		var match *storage.KbFact
		for i := range existingFacts {
			if strings.Contains(strings.ToLower(existingFacts[i].Fact), strings.ToLower(item.OldFact)) {
				match = &existingFacts[i]
				break
			}
		}
		if match == nil {
			continue
		}
		if err := c.KB.DeactivateFact(ctx, match.ID); err != nil {
			return factsAdded, profileUpdated, err
		}
		entity := item.EntityName
		if entity == nil {
			entity = match.EntityName
		}
		topic := item.Topic
		if topic == nil {
			topic = match.Topic
		}
		n, err := c.KB.InsertFacts(ctx, userID, []storage.NewFactInput{{
			Fact:         item.NewFact,
			EntityName:   entity,
			Topic:        topic,
			SupersedesID: &match.ID,
			SourceID:     optionalString(defaultSourceID),
		}})
		if err != nil {
			return factsAdded, profileUpdated, err
		}
		factsAdded += n
	}

	llmFacts := make([]storage.NewFactInput, 0)
	for _, f := range output.NewFacts {
		if strings.TrimSpace(f.Fact) == "" {
			continue
		}
		var sourceID *string
		if defaultSourceID != "" {
			sourceID = &defaultSourceID
		} else if f.SourceTurnIndex != nil && sourceByTurn != nil {
			if id, ok := sourceByTurn[*f.SourceTurnIndex]; ok {
				sourceID = &id
			}
		}
		llmFacts = append(llmFacts, storage.NewFactInput{
			Fact:       strings.TrimSpace(f.Fact),
			EntityName: f.EntityName,
			Topic:      f.Topic,
			SourceID:   sourceID,
		})
	}

	obvious := ExtractObviousFacts(obviousSources)
	existingKeys := make(map[string]struct{})
	for _, f := range existingFacts {
		existingKeys[strings.ToLower(f.Fact)] = struct{}{}
	}
	merged := append(llmFacts, obvious...)
	deduped := make([]storage.NewFactInput, 0, len(merged))
	for _, f := range merged {
		if _, ok := existingKeys[strings.ToLower(f.Fact)]; ok {
			continue
		}
		deduped = append(deduped, f)
	}

	n, err := c.KB.InsertFacts(ctx, userID, deduped)
	if err != nil {
		return factsAdded, profileUpdated, err
	}
	factsAdded += n
	return factsAdded, profileUpdated, nil
}

func parseCompilerOutput(raw string) compilerOutput {
	trimmed := strings.TrimSpace(raw)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 {
		log.Warn("compiler returned non-JSON", map[string]any{"preview": truncateStr(trimmed, 200)})
		return compilerOutput{}
	}
	var out compilerOutput
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &out); err != nil {
		log.Warn("failed to parse compiler JSON", map[string]any{
			"error":   err.Error(),
			"preview": truncateStr(trimmed, 200),
		})
		return compilerOutput{}
	}
	return out
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
