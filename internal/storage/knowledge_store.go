package storage

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

const knowledgeBucket = "knowledge-assets"

type KbSource struct {
	ID             string         `json:"id"`
	UserID         string         `json:"user_id"`
	SourceType     string         `json:"source_type"`
	Content        string         `json:"content"`
	ConversationID *string        `json:"conversation_id"`
	TurnIndex      *int           `json:"turn_index"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

type KbFact struct {
	ID         string  `json:"id"`
	UserID     string  `json:"user_id"`
	Fact       string  `json:"fact"`
	EntityName *string `json:"entity_name"`
	Topic      *string `json:"topic"`
	SourceID   *string `json:"source_id"`
	Active     bool    `json:"active"`
	CreatedAt  string  `json:"created_at,omitempty"`
}

type UserProfile struct {
	UserID        string   `json:"user_id"`
	Summary       string   `json:"summary"`
	IdentityFacts []string `json:"identity_facts"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
}

const kbFactSelect = "id,user_id,fact,entity_name,topic,source_id,active,created_at"

type ConversationTurn struct {
	TurnIndex           int    `json:"turn_index"`
	UserTranscript      string `json:"user_transcript"`
	AssistantTranscript string `json:"assistant_transcript"`
}

type NewFactInput struct {
	Fact         string
	EntityName   *string
	Topic        *string
	SourceID     *string
	SupersedesID *string
}

func (k *Knowledge) LogKnowledge(message string, data map[string]any) {
	log.Print(message, data)
}

func (k *Knowledge) UpsertUserProfileSummary(ctx context.Context, userID, summary string) error {
	body := map[string]any{
		"user_id":    userID,
		"summary":    summary,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	var rows []struct {
		UserID string `json:"user_id"`
	}
	return k.DB.Upsert(ctx, "kb_user_profiles", "user_id", body, &rows)
}

// AddIdentityFact appends a machine-detected identity fact (e.g. the user's
// name) to kb_user_profiles.identity_facts, deduped case-insensitively. These
// are always prepended to the LLM-generated summary at read time so the
// compiler's profile_summary overwrite can never silently drop them.
func (k *Knowledge) AddIdentityFact(ctx context.Context, userID, fact string) error {
	fact = strings.TrimSpace(fact)
	if fact == "" {
		return nil
	}

	q := url.Values{}
	q.Set("select", "identity_facts")
	q.Set("user_id", "eq."+userID)

	var rows []struct {
		IdentityFacts []string `json:"identity_facts"`
	}
	if err := k.DB.Get(ctx, "kb_user_profiles", q, &rows); err != nil {
		return err
	}

	existing := []string{}
	if len(rows) > 0 {
		existing = rows[0].IdentityFacts
	}
	for _, f := range existing {
		if strings.EqualFold(f, fact) {
			return nil
		}
	}
	updated := append([]string{fact}, existing...)

	patchQ := url.Values{}
	patchQ.Set("user_id", "eq."+userID)
	body := map[string]any{
		"identity_facts": updated,
		"updated_at":     time.Now().UTC().Format(time.RFC3339),
	}
	return k.DB.Patch(ctx, "kb_user_profiles", patchQ, body)
}

func (k *Knowledge) GetActiveFacts(ctx context.Context, userID string) ([]KbFact, error) {
	q := url.Values{}
	q.Set("select", kbFactSelect)
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")
	q.Set("order", "created_at.asc")

	var rows []KbFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (k *Knowledge) GetUserProfileRaw(ctx context.Context, userID string) (UserProfile, error) {
	if !k.Enabled {
		return UserProfile{UserID: userID, IdentityFacts: []string{}}, nil
	}

	q := url.Values{}
	q.Set("select", "user_id,summary,identity_facts,updated_at")
	q.Set("user_id", "eq."+userID)

	var rows []UserProfile
	if err := k.DB.Get(ctx, "kb_user_profiles", q, &rows); err != nil {
		return UserProfile{}, err
	}
	if len(rows) == 0 {
		return UserProfile{UserID: userID, IdentityFacts: []string{}}, nil
	}
	if rows[0].IdentityFacts == nil {
		rows[0].IdentityFacts = []string{}
	}
	return rows[0], nil
}

func (k *Knowledge) UpdateUserProfile(ctx context.Context, userID string, summary *string, identityFacts *[]string) (UserProfile, error) {
	if !k.Enabled {
		return UserProfile{}, fmt.Errorf("knowledge disabled")
	}

	body := map[string]any{
		"user_id":    userID,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if summary != nil {
		body["summary"] = strings.TrimSpace(*summary)
	}
	if identityFacts != nil {
		body["identity_facts"] = *identityFacts
	}

	var rows []UserProfile
	if err := k.DB.Upsert(ctx, "kb_user_profiles", "user_id", body, &rows); err != nil {
		return UserProfile{}, err
	}
	if len(rows) == 0 {
		return k.GetUserProfileRaw(ctx, userID)
	}
	if rows[0].IdentityFacts == nil {
		rows[0].IdentityFacts = []string{}
	}
	return rows[0], nil
}

func (k *Knowledge) GetFactByID(ctx context.Context, userID, factID string) (KbFact, error) {
	q := url.Values{}
	q.Set("select", kbFactSelect)
	q.Set("id", "eq."+factID)
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")

	var rows []KbFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return KbFact{}, err
	}
	if len(rows) == 0 {
		return KbFact{}, fmt.Errorf("fact not found")
	}
	return rows[0], nil
}

func (k *Knowledge) ListActiveFacts(ctx context.Context, userID string, query string, limit, offset int) ([]KbFact, error) {
	q := url.Values{}
	q.Set("select", kbFactSelect)
	q.Set("user_id", "eq."+userID)
	q.Set("active", "eq.true")
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}

	trimmed := strings.TrimSpace(query)
	if trimmed != "" {
		q.Set("search_vector", "fts.english.websearch."+trimmed)
	}

	var rows []KbFact
	if err := k.DB.Get(ctx, "kb_facts", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (k *Knowledge) InsertFactReturning(ctx context.Context, userID string, input NewFactInput) (KbFact, error) {
	var rows []KbFact
	if _, err := k.insertFacts(ctx, userID, []NewFactInput{input}, &rows); err != nil {
		return KbFact{}, err
	}
	if len(rows) == 0 {
		return KbFact{}, fmt.Errorf("failed to insert fact")
	}
	return rows[0], nil
}

func (k *Knowledge) SupersedeFact(ctx context.Context, userID, factID string, input NewFactInput) (KbFact, error) {
	existing, err := k.GetFactByID(ctx, userID, factID)
	if err != nil {
		return KbFact{}, err
	}
	if err := k.DeactivateFact(ctx, existing.ID); err != nil {
		return KbFact{}, err
	}

	supersedesID := existing.ID
	input.SupersedesID = &supersedesID
	if input.EntityName == nil {
		input.EntityName = existing.EntityName
	}
	if input.Topic == nil {
		input.Topic = existing.Topic
	}
	return k.InsertFactReturning(ctx, userID, input)
}

func (k *Knowledge) DeactivateUserFact(ctx context.Context, userID, factID string) error {
	if _, err := k.GetFactByID(ctx, userID, factID); err != nil {
		return err
	}
	return k.DeactivateFact(ctx, factID)
}

func (k *Knowledge) InsertFacts(ctx context.Context, userID string, facts []NewFactInput) (int, error) {
	return k.insertFacts(ctx, userID, facts, nil)
}

func (k *Knowledge) insertFacts(ctx context.Context, userID string, facts []NewFactInput, dest *[]KbFact) (int, error) {
	if len(facts) == 0 {
		return 0, nil
	}

	embeddings := make([][]float32, len(facts))
	if k.Embedder != nil && k.Embedder.Enabled() {
		inputs := make([]string, len(facts))
		for i, f := range facts {
			inputs[i] = factEmbeddingInput(f)
		}
		vecs, err := k.Embedder.Embed(ctx, inputs)
		if err != nil {
			log.Warn("insert facts: embedding failed, writing without vectors", map[string]any{
				"userId": log.ShortID(userID),
				"error":  err.Error(),
			})
		} else if len(vecs) == len(facts) {
			embeddings = vecs
		}
	}

	rows := make([]map[string]any, 0, len(facts))
	for i, f := range facts {
		row := map[string]any{
			"user_id":       userID,
			"fact":          f.Fact,
			"entity_name":   f.EntityName,
			"topic":         f.Topic,
			"source_id":     f.SourceID,
			"supersedes_id": f.SupersedesID,
			"active":        true,
		}
		if embeddings[i] != nil {
			row["embedding"] = embeddings[i]
		}
		rows = append(rows, row)
	}
	if err := k.DB.Insert(ctx, "kb_facts", rows, dest); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// factEmbeddingInput builds the text that gets embedded for a fact. Including
// entity_name and topic improves recall for entity-centric queries.
func factEmbeddingInput(f NewFactInput) string {
	parts := make([]string, 0, 3)
	if f.EntityName != nil && strings.TrimSpace(*f.EntityName) != "" {
		parts = append(parts, *f.EntityName)
	}
	if f.Topic != nil && strings.TrimSpace(*f.Topic) != "" {
		parts = append(parts, *f.Topic)
	}
	parts = append(parts, f.Fact)
	return strings.Join(parts, " ")
}

func (k *Knowledge) DeactivateFact(ctx context.Context, factID string) error {
	q := url.Values{}
	q.Set("id", "eq."+factID)
	return k.DB.Patch(ctx, "kb_facts", q, map[string]any{"active": false})
}

func (k *Knowledge) GetConversationTurns(ctx context.Context, conversationID string) ([]ConversationTurn, error) {
	q := url.Values{}
	q.Set("select", "turn_index,user_transcript,assistant_transcript")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("order", "turn_index.asc")

	var rows []ConversationTurn
	if err := k.DB.Get(ctx, "conversation_turns", q, &rows); err != nil {
		return nil, fmt.Errorf("failed to load conversation turns: %w", err)
	}
	return rows, nil
}

const (
	turnSettleInterval = 3 * time.Second
	turnWaitTimeout    = 60 * time.Second
)

func (k *Knowledge) isConversationEnded(ctx context.Context, conversationID string) (bool, error) {
	q := url.Values{}
	q.Set("select", "ended_at")
	q.Set("id", "eq."+conversationID)

	var rows []struct {
		EndedAt *string `json:"ended_at"`
	}
	if err := k.DB.Get(ctx, "conversations", q, &rows); err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	return rows[0].EndedAt != nil && strings.TrimSpace(*rows[0].EndedAt) != "", nil
}

// WaitForConversationTurns blocks until the conversation has ended and all async
// turn writes have settled. Previously this returned as soon as the first turn
// appeared, causing the compiler to process only turn 0.
func (k *Knowledge) WaitForConversationTurns(ctx context.Context, conversationID string) ([]ConversationTurn, error) {
	deadline := time.Now().Add(turnWaitTimeout)

	for time.Now().Before(deadline) {
		ended, err := k.isConversationEnded(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		if ended {
			break
		}
		if err := sleepOrDone(ctx, time.Second); err != nil {
			return k.GetConversationTurns(ctx, conversationID)
		}
	}

	var (
		lastCount   = -1
		stableSince time.Time
	)
	for time.Now().Before(deadline) {
		turns, err := k.GetConversationTurns(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		count := len(turns)
		if count == lastCount {
			if count > 0 && time.Since(stableSince) >= turnSettleInterval {
				log.Print("conversation turns settled", map[string]any{
					"conversationId": conversationID,
					"turns":          count,
				})
				return turns, nil
			}
		} else {
			lastCount = count
			stableSince = time.Now()
		}
		if err := sleepOrDone(ctx, time.Second); err != nil {
			return k.GetConversationTurns(ctx, conversationID)
		}
	}

	turns, err := k.GetConversationTurns(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	log.Warn("conversation turn wait timed out", map[string]any{
		"conversationId": conversationID,
		"turns":          len(turns),
	})
	return turns, nil
}

func sleepOrDone(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (k *Knowledge) UpsertVoiceSource(ctx context.Context, input struct {
	UserID, ConversationID, UserTranscript, AssistantTranscript string
	TurnIndex                                                   int
}) (string, error) {
	content := "User: " + input.UserTranscript + "\nAssistant: " + input.AssistantTranscript
	body := map[string]any{
		"user_id":         input.UserID,
		"source_type":     "voice_turn",
		"content":         content,
		"conversation_id": input.ConversationID,
		"turn_index":      input.TurnIndex,
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := k.DB.Upsert(ctx, "kb_sources", "conversation_id,turn_index", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to upsert kb_source")
	}
	return rows[0].ID, nil
}

func (k *Knowledge) GetSourcesForConversation(ctx context.Context, conversationID string) ([]KbSource, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,source_type,content,conversation_id,turn_index,created_at")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("order", "turn_index.asc")

	var rows []KbSource
	if err := k.DB.Get(ctx, "kb_sources", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (k *Knowledge) SyncConversationSources(ctx context.Context, userID, conversationID string) ([]KbSource, error) {
	turns, err := k.GetConversationTurns(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	for _, turn := range turns {
		_, err := k.UpsertVoiceSource(ctx, struct {
			UserID, ConversationID, UserTranscript, AssistantTranscript string
			TurnIndex                                                   int
		}{
			UserID:              userID,
			ConversationID:      conversationID,
			TurnIndex:           turn.TurnIndex,
			UserTranscript:      turn.UserTranscript,
			AssistantTranscript: turn.AssistantTranscript,
		})
		if err != nil {
			return nil, err
		}
	}
	return k.GetSourcesForConversation(ctx, conversationID)
}

func (k *Knowledge) latestCompiledTurnCount(ctx context.Context, conversationID string) (int, error) {
	q := url.Values{}
	q.Set("select", "turns_count")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("status", "eq.completed")
	q.Set("order", "created_at.desc")
	q.Set("limit", "1")

	var rows []struct {
		TurnsCount *int `json:"turns_count"`
	}
	if err := k.DB.Get(ctx, "kb_compile_log", q, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 || rows[0].TurnsCount == nil {
		return 0, nil
	}
	return *rows[0].TurnsCount, nil
}

func conversationFullyCompiled(compiledTurns, currentTurns int) bool {
	return compiledTurns > 0 && currentTurns > 0 && compiledTurns >= currentTurns
}

func (k *Knowledge) IsConversationCompiled(ctx context.Context, conversationID string) (bool, error) {
	compiledTurns, err := k.latestCompiledTurnCount(ctx, conversationID)
	if err != nil {
		log.Warn("failed to check compile status", map[string]any{"error": err.Error()})
		return false, nil
	}
	if compiledTurns == 0 {
		return false, nil
	}

	turns, err := k.GetConversationTurns(ctx, conversationID)
	if err != nil {
		log.Warn("failed to count conversation turns for compile check", map[string]any{"error": err.Error()})
		return false, nil
	}
	return conversationFullyCompiled(compiledTurns, len(turns)), nil
}

func (k *Knowledge) CreateCompileLog(ctx context.Context, userID, conversationID string) (string, error) {
	var rows []struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"user_id":         userID,
		"conversation_id": conversationID,
		"status":          "running",
	}
	if err := k.DB.Insert(ctx, "kb_compile_log", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to create compile log")
	}
	return rows[0].ID, nil
}

func (k *Knowledge) CompleteCompileLog(ctx context.Context, logID, status string, turnsCount, factsAdded int, errMsg string) {
	q := url.Values{}
	q.Set("id", "eq."+logID)
	body := map[string]any{
		"status":       status,
		"turns_count":  turnsCount,
		"facts_added":  factsAdded,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
	}
	if errMsg != "" {
		body["error"] = errMsg
	} else {
		body["error"] = nil
	}
	if err := k.DB.Patch(ctx, "kb_compile_log", q, body); err != nil {
		log.Warn("failed to update compile log", map[string]any{"error": err.Error()})
	}
}

// RecordSupersedeMisses stores dropped supersede attempts on the compile log
// for debugging. The supersede_misses column is a jsonb array.
func (k *Knowledge) RecordSupersedeMisses(ctx context.Context, logID string, misses []map[string]any) error {
	q := url.Values{}
	q.Set("id", "eq."+logID)
	body := map[string]any{
		"supersede_misses": misses,
	}
	return k.DB.Patch(ctx, "kb_compile_log", q, body)
}

func (k *Knowledge) InsertAssetSource(ctx context.Context, userID, content string, metadata map[string]any) (string, error) {
	var rows []struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"user_id":     userID,
		"source_type": "document",
		"content":     content,
		"metadata":    metadata,
	}
	if err := k.DB.Insert(ctx, "kb_sources", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to insert asset source")
	}
	return rows[0].ID, nil
}

func (k *Knowledge) GetSourceByID(ctx context.Context, sourceID string) (KbSource, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,source_type,content,conversation_id,turn_index,metadata,created_at")
	q.Set("id", "eq."+sourceID)

	var rows []KbSource
	if err := k.DB.Get(ctx, "kb_sources", q, &rows); err != nil {
		return KbSource{}, err
	}
	if len(rows) == 0 {
		return KbSource{}, fmt.Errorf("source not found")
	}
	return rows[0], nil
}

// ConversationAudioBucket is the storage bucket holding the user's recorded
// speech for talk-mode voice turns — the same audio the iOS/web mic button
// captures when chatting with Donna. Layout: `<userID>/<conversationID>/<turnIndex>/user.wav`.
const ConversationAudioBucket = "conversation-audio"

// PathOfTalkUserAudio returns the storage object key for the user's recorded
// speech for a single talk-mode voice turn. Returns "" when source lacks the
// conversation/turn pointer (text channel rows, partial imports, etc).
func PathOfTalkUserAudio(source KbSource) string {
	if source.ConversationID == nil || strings.TrimSpace(*source.ConversationID) == "" {
		return ""
	}
	if source.TurnIndex == nil {
		return ""
	}
	if strings.TrimSpace(source.UserID) == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%d/user.wav", source.UserID, *source.ConversationID, *source.TurnIndex)
}

// SignedTalkUserAudioURL fetches the kb_sources row for the given note's
// source_id and returns a short-lived signed URL for the user's recorded
// speech in the conversation-audio bucket. Returns "" (no error) when the
// note has no source, the source isn't a voice turn, or there's no audio
// pointer stored — callers fall back to text-only rendering.
const talkAudioSignedURLTTL = 30 * time.Minute

func (k *Knowledge) SignedTalkUserAudioURL(ctx context.Context, note Note) string {
	if k == nil || k.DB == nil || !k.DB.Enabled() {
		return ""
	}
	if note.SourceType != "voice_turn" || note.SourceID == nil || strings.TrimSpace(*note.SourceID) == "" {
		return ""
	}
	source, err := k.GetSourceByID(ctx, *note.SourceID)
	if err != nil {
		log.Warn("failed to load kb_source for note audio", map[string]any{
			"noteId":   note.ID,
			"sourceId": *note.SourceID,
			"error":    err.Error(),
		})
		return ""
	}
	if source.SourceType != "voice_turn" {
		return ""
	}
	objectPath := PathOfTalkUserAudio(source)
	if objectPath == "" {
		return ""
	}
	signed, err := k.DB.CreateSignedURL(ctx, ConversationAudioBucket, objectPath, talkAudioSignedURLTTL)
	if err != nil {
		log.Warn("failed to sign talk user audio url", map[string]any{
			"noteId":    note.ID,
			"audioPath": objectPath,
			"error":     err.Error(),
		})
		return ""
	}
	return signed
}

func (k *Knowledge) IsSourceCompiled(ctx context.Context, sourceID string) (bool, error) {
	q := url.Values{}
	q.Set("select", "id")
	q.Set("source_id", "eq."+sourceID)
	q.Set("active", "eq.true")
	count, err := k.DB.Count(ctx, "kb_facts", q)
	if err != nil {
		log.Warn("failed to check source facts", map[string]any{"sourceId": sourceID, "error": err.Error()})
		return false, nil
	}
	return count > 0, nil
}

func (k *Knowledge) MarkSourceCompiled(ctx context.Context, sourceID string) error {
	source, err := k.GetSourceByID(ctx, sourceID)
	if err != nil {
		return err
	}
	metadata := map[string]any{}
	for k, v := range source.Metadata {
		metadata[k] = v
	}
	metadata["compiled_at"] = time.Now().UTC().Format(time.RFC3339)

	q := url.Values{}
	q.Set("id", "eq."+sourceID)
	return k.DB.Patch(ctx, "kb_sources", q, map[string]any{"metadata": metadata})
}

func (k *Knowledge) UploadAssetFile(ctx context.Context, userID, filename string, data []byte, mimeType string) (string, error) {
	safeName := regexp.MustCompile(`[^a-zA-Z0-9._-]`).ReplaceAllString(filename, "_")
	path := fmt.Sprintf("%s/%d-%s", userID, time.Now().UnixMilli(), safeName)
	if err := k.DB.UploadStorage(ctx, knowledgeBucket, path, mimeType, data); err != nil {
		return "", err
	}
	return path, nil
}
