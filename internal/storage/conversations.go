package storage

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

const audioBucket = "conversation-audio"

type Conversations struct {
	DB       *Supabase
	Enabled  bool
	TitleGen TitleGenerator // optional; generates LLM titles after the first turn
}

type SaveTurnInput struct {
	ConversationID       string
	UserID             string
	TurnIndex          int
	UserTranscript     string
	AssistantTranscript string
	UserWav            []byte
	AssistantAudio     []byte
	AssistantFormat    string
	Timings            protocol.TurnTimings
}

func (c *Conversations) Create(ctx context.Context, userID, voiceSessionID string) (string, error) {
	var rows []struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"user_id":          userID,
		"voice_session_id": voiceSessionID,
		"channel":          "voice",
	}
	if err := c.DB.Insert(ctx, "conversations", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to create conversation")
	}

	log.Print("conversation created", map[string]any{
		"conversationId": rows[0].ID,
		"voiceSessionId": voiceSessionID,
		"userId":         log.ShortID(userID),
	})
	return rows[0].ID, nil
}

func (c *Conversations) GetOrCreateText(ctx context.Context, userID, clientSessionID string) (string, error) {
	if !c.Enabled {
		return "", fmt.Errorf("conversation persistence disabled")
	}
	clientSessionID = strings.TrimSpace(clientSessionID)
	if clientSessionID == "" {
		return "", fmt.Errorf("missing client session id")
	}

	q := url.Values{}
	q.Set("select", "id")
	q.Set("user_id", "eq."+userID)
	q.Set("channel", "eq.text")
	q.Set("client_session_id", "eq."+clientSessionID)
	q.Set("ended_at", "is.null")
	q.Set("limit", "1")

	var existing []struct {
		ID string `json:"id"`
	}
	if err := c.DB.Get(ctx, "conversations", q, &existing); err != nil {
		return "", err
	}
	if len(existing) > 0 {
		return existing[0].ID, nil
	}

	var rows []struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"user_id":            userID,
		"channel":            "text",
		"client_session_id":  clientSessionID,
	}
	if err := c.DB.Insert(ctx, "conversations", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to create text conversation")
	}

	log.Print("text conversation created", map[string]any{
		"conversationId":  rows[0].ID,
		"clientSessionId": clientSessionID,
		"userId":          log.ShortID(userID),
	})
	return rows[0].ID, nil
}

func (c *Conversations) End(ctx context.Context, conversationID string) error {
	q := url.Values{}
	q.Set("id", "eq."+conversationID)
	q.Set("ended_at", "is.null")

	body := map[string]string{
		"ended_at": time.Now().UTC().Format(time.RFC3339),
	}
	if err := c.DB.Patch(ctx, "conversations", q, body); err != nil {
		return err
	}
	log.Print("conversation ended", map[string]any{"conversationId": conversationID})
	return nil
}

func audioPaths(userID, conversationID string, turnIndex int, assistantFormat string) (userPath, assistantPath string) {
	base := fmt.Sprintf("%s/%s/%d", userID, conversationID, turnIndex)
	return base + "/user.wav", base + "/assistant." + assistantFormat
}

func assistantMime(format string) string {
	if format == "mp3" {
		return "audio/mpeg"
	}
	return "audio/wav"
}

func (c *Conversations) SaveTurn(ctx context.Context, input SaveTurnInput) error {
	assistantFormat := input.AssistantFormat
	if assistantFormat == "" {
		assistantFormat = "wav"
	}

	var userPath, assistantPath string
	if len(input.UserWav) > 0 {
		userPath, assistantPath = audioPaths(input.UserID, input.ConversationID, input.TurnIndex, assistantFormat)
		if err := c.DB.UploadStorage(ctx, audioBucket, userPath, "audio/wav", input.UserWav); err != nil {
			log.Warn("user audio upload failed — saving transcript only", map[string]any{
				"conversationId": input.ConversationID,
				"turnIndex":      input.TurnIndex,
				"error":          err.Error(),
			})
			userPath = ""
		}
	}
	if len(input.AssistantAudio) > 0 && userPath != "" {
		_, assistantPath = audioPaths(input.UserID, input.ConversationID, input.TurnIndex, assistantFormat)
		if err := c.DB.UploadStorage(ctx, audioBucket, assistantPath, assistantMime(assistantFormat), input.AssistantAudio); err != nil {
			log.Warn("assistant audio upload failed — saving transcript only", map[string]any{
				"conversationId": input.ConversationID,
				"turnIndex":      input.TurnIndex,
				"error":          err.Error(),
			})
			assistantPath = ""
		}
	} else {
		assistantPath = ""
	}

	body := map[string]any{
		"conversation_id":      input.ConversationID,
		"turn_index":           input.TurnIndex,
		"user_transcript":      input.UserTranscript,
		"assistant_transcript": input.AssistantTranscript,
		"timings":              input.Timings,
	}
	if userPath != "" {
		body["user_audio_path"] = userPath
		body["user_audio_mime"] = "audio/wav"
	}
	if assistantPath != "" {
		body["assistant_audio_path"] = assistantPath
		body["assistant_audio_mime"] = assistantMime(assistantFormat)
	}
	// Upsert so regenerate / retry can replace the same turn_index without
	// hitting the unique (conversation_id, turn_index) constraint.
	if err := c.DB.Upsert(ctx, "conversation_turns", "conversation_id,turn_index", body, nil); err != nil {
		return err
	}

	if title := deriveConversationTitle(input.UserTranscript, input.AssistantTranscript); title != "" {
		if err := c.setTitleIfEmpty(ctx, input.ConversationID, title); err != nil {
			log.Warn("failed to set conversation title", map[string]any{
				"conversationId": input.ConversationID,
				"turnIndex":      input.TurnIndex,
				"error":          err.Error(),
			})
		}
	}

	// After the first turn, try an async LLM title without blocking persistence.
	if input.TurnIndex == 0 {
		c.maybeGenerateTitleAsync(input.ConversationID, input.UserTranscript, input.AssistantTranscript)
	}

	log.Print("turn saved", map[string]any{
		"conversationId":     input.ConversationID,
		"turnIndex":          input.TurnIndex,
		"userAudioPath":      userPath,
		"assistantAudioPath": assistantPath,
	})
	return nil
}

func (c *Conversations) PersistTurnAsync(input SaveTurnInput) {
	if !c.Enabled {
		return
	}
	go func() {
		if err := c.SaveTurn(context.Background(), input); err != nil {
			log.Warn("failed to persist turn", map[string]any{
				"conversationId": input.ConversationID,
				"turnIndex":      input.TurnIndex,
				"error":          err.Error(),
			})
		}
	}()
}

func (c *Conversations) EndAsync(conversationID string) {
	if !c.Enabled {
		return
	}
	go func() {
		ctx := context.Background()
		if err := c.ensureTitleFromTurns(ctx, conversationID); err != nil {
			log.Warn("failed to ensure conversation title", map[string]any{
				"conversationId": conversationID,
				"error":          err.Error(),
			})
		}
		if err := c.End(ctx, conversationID); err != nil {
			log.Warn("failed to end conversation", map[string]any{
				"conversationId": conversationID,
				"error":          err.Error(),
			})
		}
	}()
}

type ConversationSummary struct {
	ID              string   `json:"id"`
	Channel         string   `json:"channel"`
	Title           string   `json:"title"`
	TitleSource     string   `json:"title_source,omitempty"`
	ClientSessionID *string  `json:"client_session_id,omitempty"`
	VoiceSessionID  *string  `json:"voice_session_id,omitempty"`
	Preview         string   `json:"preview"`
	TurnCount       int      `json:"turn_count"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	EndedAt         *string  `json:"ended_at,omitempty"`
	ArchivedAt      *string  `json:"archived_at,omitempty"`
	PinnedAt        *string  `json:"pinned_at,omitempty"`
	Tags            []string `json:"tags,omitempty"`
}

type StoredTurn struct {
	TurnIndex           int    `json:"turn_index"`
	UserTranscript      string `json:"user_transcript"`
	AssistantTranscript string `json:"assistant_transcript"`
	CreatedAt           string `json:"created_at"`
}

type ConversationDetail struct {
	ID              string       `json:"id"`
	Channel         string       `json:"channel"`
	Title           string       `json:"title"`
	TitleSource     string       `json:"title_source,omitempty"`
	ClientSessionID *string      `json:"client_session_id,omitempty"`
	VoiceSessionID  *string      `json:"voice_session_id,omitempty"`
	CreatedAt       string       `json:"created_at"`
	EndedAt         *string      `json:"ended_at,omitempty"`
	ArchivedAt      *string      `json:"archived_at,omitempty"`
	PinnedAt        *string      `json:"pinned_at,omitempty"`
	Tags            []string     `json:"tags,omitempty"`
	Turns           []StoredTurn `json:"turns"`
}

// ListForUser returns conversation summaries for the user.
// By default archived chats are hidden. Pass opts.Query for keyword search.
func (c *Conversations) ListForUser(ctx context.Context, userID string, opts ListOptions) ([]ConversationSummary, error) {
	if !c.Enabled {
		return nil, fmt.Errorf("conversation persistence disabled")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	var filterIDs []string
	if q := strings.TrimSpace(opts.Query); q != "" {
		ids, err := c.searchConversationIDs(ctx, userID, ListOptions{
			Limit:           limit,
			IncludeArchived: opts.IncludeArchived || opts.ArchivedOnly,
			Query:           q,
		})
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []ConversationSummary{}, nil
		}
		filterIDs = ids
	}
	if tag := strings.TrimSpace(opts.Tag); tag != "" {
		ids, err := c.conversationIDsForTag(ctx, userID, tag)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []ConversationSummary{}, nil
		}
		if filterIDs == nil {
			filterIDs = ids
		} else {
			filterIDs = intersectIDs(filterIDs, ids)
			if len(filterIDs) == 0 {
				return []ConversationSummary{}, nil
			}
		}
	}

	q := url.Values{}
	q.Set("select", "id,channel,title,title_source,client_session_id,voice_session_id,created_at,ended_at,archived_at,pinned_at")
	q.Set("user_id", "eq."+userID)
	if opts.ArchivedOnly {
		q.Set("archived_at", "not.is.null")
	} else if !opts.IncludeArchived {
		q.Set("archived_at", "is.null")
	}
	if filterIDs != nil {
		q.Set("id", "in.("+strings.Join(filterIDs, ",")+")")
	}
	q.Set("order", "created_at.desc")
	// Over-fetch slightly when filtering empty chats client-side after turn load.
	fetchLimit := limit
	if filterIDs == nil {
		fetchLimit = limit * 2
		if fetchLimit > 200 {
			fetchLimit = 200
		}
	}
	q.Set("limit", fmt.Sprintf("%d", fetchLimit))

	var rows []struct {
		ID              string  `json:"id"`
		Channel         string  `json:"channel"`
		Title           string  `json:"title"`
		TitleSource     string  `json:"title_source"`
		ClientSessionID *string `json:"client_session_id"`
		VoiceSessionID  *string `json:"voice_session_id"`
		CreatedAt       string  `json:"created_at"`
		EndedAt         *string `json:"ended_at"`
		ArchivedAt      *string `json:"archived_at"`
		PinnedAt        *string `json:"pinned_at"`
	}
	if err := c.DB.Get(ctx, "conversations", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []ConversationSummary{}, nil
	}

	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}

	turnsByConversation, err := c.loadTurnsForConversations(ctx, ids)
	if err != nil {
		return nil, err
	}
	tagsByConversation, err := c.getTagsForConversations(ctx, userID, ids)
	if err != nil {
		return nil, err
	}

	summaries := make([]ConversationSummary, 0, len(rows))
	for _, row := range rows {
		turns := turnsByConversation[row.ID]
		preview := ""
		updatedAt := row.CreatedAt
		if len(turns) > 0 {
			preview = truncatePreview(latestTurnSnippet(turns))
			updatedAt = turns[len(turns)-1].CreatedAt
		}
		title := strings.TrimSpace(row.Title)
		if title == "" && len(turns) > 0 {
			title = deriveConversationTitle(turns[0].UserTranscript, turns[0].AssistantTranscript)
		}
		if title == "" {
			title = fallbackConversationTitle(row.Channel, row.CreatedAt)
		}
		if len(turns) == 0 {
			continue
		}
		tags := tagsByConversation[row.ID]
		if tags == nil {
			tags = []string{}
		}
		summaries = append(summaries, ConversationSummary{
			ID:              row.ID,
			Channel:         row.Channel,
			Title:           title,
			TitleSource:     row.TitleSource,
			ClientSessionID: row.ClientSessionID,
			VoiceSessionID:  row.VoiceSessionID,
			Preview:         preview,
			TurnCount:       len(turns),
			CreatedAt:       row.CreatedAt,
			UpdatedAt:       updatedAt,
			EndedAt:         row.EndedAt,
			ArchivedAt:      row.ArchivedAt,
			PinnedAt:        row.PinnedAt,
			Tags:            tags,
		})
		if len(summaries) >= limit {
			break
		}
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		pi, pj := summaries[i].PinnedAt != nil, summaries[j].PinnedAt != nil
		if pi != pj {
			return pi
		}
		if pi && pj && summaries[i].PinnedAt != nil && summaries[j].PinnedAt != nil {
			if *summaries[i].PinnedAt != *summaries[j].PinnedAt {
				return *summaries[i].PinnedAt > *summaries[j].PinnedAt
			}
		}
		return summaries[i].UpdatedAt > summaries[j].UpdatedAt
	})

	return summaries, nil
}

func intersectIDs(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, id := range b {
		set[id] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, id := range a {
		if _, ok := set[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// FindTextByClientSession returns the conversation id for a text chat session,
// or "" if none exists yet.
func (c *Conversations) FindTextByClientSession(ctx context.Context, userID, clientSessionID string) (string, error) {
	if !c.Enabled {
		return "", fmt.Errorf("conversation persistence disabled")
	}
	clientSessionID = strings.TrimSpace(clientSessionID)
	if clientSessionID == "" {
		return "", fmt.Errorf("missing client session id")
	}

	q := url.Values{}
	q.Set("select", "id")
	q.Set("user_id", "eq."+userID)
	q.Set("channel", "eq.text")
	q.Set("client_session_id", "eq."+clientSessionID)
	q.Set("order", "created_at.desc")
	q.Set("limit", "1")

	var rows []struct {
		ID string `json:"id"`
	}
	if err := c.DB.Get(ctx, "conversations", q, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].ID, nil
}

// DeleteTurnsFrom removes persisted turns with turn_index >= fromIndex for the
// text conversation identified by client_session_id. Used by edit/resend and
// regenerate so re-posts do not collide on turn_index.
func (c *Conversations) DeleteTurnsFrom(ctx context.Context, userID, clientSessionID string, fromIndex int) error {
	if !c.Enabled {
		return fmt.Errorf("conversation persistence disabled")
	}
	if fromIndex < 0 {
		return fmt.Errorf("invalid from_index")
	}

	conversationID, err := c.FindTextByClientSession(ctx, userID, clientSessionID)
	if err != nil {
		return err
	}
	if conversationID == "" {
		// Nothing persisted yet — treat as success so clients can truncate UI-only.
		return nil
	}

	q := url.Values{}
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("turn_index", "gte."+fmt.Sprintf("%d", fromIndex))
	if err := c.DB.Delete(ctx, "conversation_turns", q); err != nil {
		return err
	}

	log.Print("turns truncated", map[string]any{
		"conversationId": conversationID,
		"fromIndex":      fromIndex,
		"userId":         log.ShortID(userID),
	})
	return nil
}

type TurnFeedbackInput struct {
	UserID          string
	ClientSessionID string
	TurnIndex       int
	Rating          string // "up" | "down"
	Comment         string
}

// UpsertTurnFeedback stores thumbs up/down for a turn. Resolves the conversation
// via client_session_id. Fails if the conversation does not exist yet.
func (c *Conversations) UpsertTurnFeedback(ctx context.Context, input TurnFeedbackInput) error {
	if !c.Enabled {
		return fmt.Errorf("conversation persistence disabled")
	}
	rating := strings.TrimSpace(input.Rating)
	if rating != "up" && rating != "down" {
		return fmt.Errorf("rating must be up or down")
	}
	if input.TurnIndex < 0 {
		return fmt.Errorf("invalid turn_index")
	}

	conversationID, err := c.FindTextByClientSession(ctx, input.UserID, input.ClientSessionID)
	if err != nil {
		return err
	}
	if conversationID == "" {
		return fmt.Errorf("conversation not found")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{
		"user_id":         input.UserID,
		"conversation_id": conversationID,
		"turn_index":      input.TurnIndex,
		"rating":          rating,
		"comment":         strings.TrimSpace(input.Comment),
		"updated_at":      now,
	}
	if err := c.DB.Upsert(ctx, "conversation_turn_feedback", "conversation_id,turn_index", body, nil); err != nil {
		return err
	}
	return nil
}

func (c *Conversations) GetWithTurns(ctx context.Context, userID, conversationID string) (*ConversationDetail, error) {
	if !c.Enabled {
		return nil, fmt.Errorf("conversation persistence disabled")
	}

	q := url.Values{}
	q.Set("select", "id,channel,title,title_source,client_session_id,voice_session_id,created_at,ended_at,archived_at,pinned_at")
	q.Set("id", "eq."+conversationID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")

	var rows []struct {
		ID              string  `json:"id"`
		Channel         string  `json:"channel"`
		Title           string  `json:"title"`
		TitleSource     string  `json:"title_source"`
		ClientSessionID *string `json:"client_session_id"`
		VoiceSessionID  *string `json:"voice_session_id"`
		CreatedAt       string  `json:"created_at"`
		EndedAt         *string `json:"ended_at"`
		ArchivedAt      *string `json:"archived_at"`
		PinnedAt        *string `json:"pinned_at"`
	}
	if err := c.DB.Get(ctx, "conversations", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	turns, err := c.loadTurns(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	tags, err := c.getTagsForConversation(ctx, userID, conversationID)
	if err != nil {
		return nil, err
	}

	row := rows[0]
	title := strings.TrimSpace(row.Title)
	if title == "" && len(turns) > 0 {
		title = deriveConversationTitle(turns[0].UserTranscript, turns[0].AssistantTranscript)
	}
	if title == "" {
		title = fallbackConversationTitle(row.Channel, row.CreatedAt)
	}
	return &ConversationDetail{
		ID:              row.ID,
		Channel:         row.Channel,
		Title:           title,
		TitleSource:     row.TitleSource,
		ClientSessionID: row.ClientSessionID,
		VoiceSessionID:  row.VoiceSessionID,
		CreatedAt:       row.CreatedAt,
		EndedAt:         row.EndedAt,
		ArchivedAt:      row.ArchivedAt,
		PinnedAt:        row.PinnedAt,
		Tags:            tags,
		Turns:           turns,
	}, nil
}

func (c *Conversations) loadTurns(ctx context.Context, conversationID string) ([]StoredTurn, error) {
	q := url.Values{}
	q.Set("select", "turn_index,user_transcript,assistant_transcript,created_at")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("order", "turn_index.asc")

	var rows []StoredTurn
	if err := c.DB.Get(ctx, "conversation_turns", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (c *Conversations) loadTurnsForConversations(ctx context.Context, conversationIDs []string) (map[string][]StoredTurn, error) {
	result := make(map[string][]StoredTurn, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return result, nil
	}

	q := url.Values{}
	q.Set("select", "conversation_id,turn_index,user_transcript,assistant_transcript,created_at")
	q.Set("conversation_id", "in.("+strings.Join(conversationIDs, ",")+")")
	q.Set("order", "turn_index.asc")

	var rows []struct {
		ConversationID      string `json:"conversation_id"`
		TurnIndex           int    `json:"turn_index"`
		UserTranscript      string `json:"user_transcript"`
		AssistantTranscript string `json:"assistant_transcript"`
		CreatedAt           string `json:"created_at"`
	}
	if err := c.DB.Get(ctx, "conversation_turns", q, &rows); err != nil {
		return nil, err
	}

	for _, row := range rows {
		result[row.ConversationID] = append(result[row.ConversationID], StoredTurn{
			TurnIndex:           row.TurnIndex,
			UserTranscript:      row.UserTranscript,
			AssistantTranscript: row.AssistantTranscript,
			CreatedAt:           row.CreatedAt,
		})
	}
	return result, nil
}

func truncatePreview(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) <= 80 {
		return text
	}
	return text[:77] + "..."
}

func deriveConversationTitle(userTranscript, assistantTranscript string) string {
	if title := truncatePreview(userTranscript); title != "" {
		return title
	}
	return truncatePreview(assistantTranscript)
}

func latestTurnSnippet(turns []StoredTurn) string {
	if len(turns) == 0 {
		return ""
	}
	last := turns[len(turns)-1]
	if snippet := strings.TrimSpace(last.UserTranscript); snippet != "" {
		return snippet
	}
	return last.AssistantTranscript
}

func fallbackConversationTitle(channel, createdAt string) string {
	label := "Chat"
	if channel == "voice" {
		label = "Voice chat"
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return label
	}
	return fmt.Sprintf("%s · %s", label, t.Format("Jan 2"))
}

func (c *Conversations) setTitle(ctx context.Context, conversationID, title string) error {
	q := url.Values{}
	q.Set("id", "eq."+conversationID)
	return c.DB.Patch(ctx, "conversations", q, map[string]string{
		"title":        title,
		"title_source": "auto",
	})
}

func (c *Conversations) setTitleIfEmpty(ctx context.Context, conversationID, title string) error {
	q := url.Values{}
	q.Set("id", "eq."+conversationID)
	q.Set("title", "eq.")
	return c.DB.Patch(ctx, "conversations", q, map[string]string{
		"title":        title,
		"title_source": "auto",
	})
}

// RecentUserTurns returns the most recent user transcripts across all of the
// user's conversations (newest first), capped at limit. Used to give the daily
// plan LLM context about what the user has been talking about lately — mirroring
// Steve's daily plan which pulls the recent transcript.
func (c *Conversations) RecentUserTurns(ctx context.Context, userID string, limit int) ([]string, error) {
	if !c.Enabled {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	// Two-step via PostgREST: latest conversations for the user, then their turns.
	convQ := url.Values{}
	convQ.Set("select", "id")
	convQ.Set("user_id", "eq."+userID)
	convQ.Set("order", "created_at.desc")
	convQ.Set("limit", "10")

	var convRows []struct {
		ID string `json:"id"`
	}
	if err := c.DB.Get(ctx, "conversations", convQ, &convRows); err != nil {
		return nil, err
	}
	if len(convRows) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(convRows))
	for _, r := range convRows {
		ids = append(ids, r.ID)
	}

	turnQ := url.Values{}
	turnQ.Set("select", "user_transcript,created_at")
	turnQ.Set("conversation_id", "in.("+strings.Join(ids, ",")+")")
	turnQ.Set("order", "created_at.desc")
	turnQ.Set("limit", fmt.Sprintf("%d", limit))

	var rows []struct {
		UserTranscript string `json:"user_transcript"`
		CreatedAt      string `json:"created_at"`
	}
	if err := c.DB.Get(ctx, "conversation_turns", turnQ, &rows); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(rows))
	for _, r := range rows {
		t := strings.TrimSpace(r.UserTranscript)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (c *Conversations) ensureTitleFromTurns(ctx context.Context, conversationID string) error {
	q := url.Values{}
	q.Set("select", "title,channel,created_at")
	q.Set("id", "eq."+conversationID)
	q.Set("limit", "1")

	var rows []struct {
		Title     string `json:"title"`
		Channel   string `json:"channel"`
		CreatedAt string `json:"created_at"`
	}
	if err := c.DB.Get(ctx, "conversations", q, &rows); err != nil {
		return err
	}
	if len(rows) == 0 || strings.TrimSpace(rows[0].Title) != "" {
		return nil
	}

	turns, err := c.loadTurns(ctx, conversationID)
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		return nil
	}

	title := deriveConversationTitle(turns[0].UserTranscript, turns[0].AssistantTranscript)
	if title == "" {
		title = fallbackConversationTitle(rows[0].Channel, rows[0].CreatedAt)
	}
	if title == "" {
		return nil
	}
	return c.setTitle(ctx, conversationID, title)
}
