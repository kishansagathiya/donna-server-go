package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type Note struct {
	ID               string  `json:"id"`
	UserID           string  `json:"user_id"`
	SourceID         *string `json:"source_id"`
	SourceType       string  `json:"source_type"`
	NoteDate         string  `json:"note_date"`
	Title            string  `json:"title"`
	Content          string  `json:"content"`
	Preview          string  `json:"preview"`
	IsImportant      bool    `json:"is_important"`
	IsUrgent         bool    `json:"is_urgent"`
	UserLastModified *string `json:"user_last_modified"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

type NoteSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Preview     string `json:"preview"`
	NoteDate    string `json:"note_date"`
	IsImportant bool   `json:"is_important"`
	IsUrgent    bool   `json:"is_urgent"`
	SourceType  string `json:"source_type"`
}

type NoteFlags struct {
	IsImportant *bool
	IsUrgent    *bool
}

type NoteUpdate struct {
	Content   *string
	NoteDate  *string
	IsImportant *bool
	IsUrgent    *bool
}

type Notes struct {
	DB       *Supabase
	Enabled  bool
	Embedder Embedder
}

func (n *Notes) selectColumns() string {
	return "id,user_id,source_id,source_type,note_date,title,content,preview,is_important,is_urgent,user_last_modified,created_at,updated_at"
}

func (n *Notes) summaryColumns() string {
	return "id,title,preview,note_date,is_important,is_urgent,source_type"
}

func (n *Notes) CreateNote(ctx context.Context, userID, sourceType, content string, opts struct {
	SourceID *string
	NoteDate *time.Time
}) (Note, error) {
	title := noteTitle(content)
	preview := notePreview(content)
	noteDate := time.Now().UTC()
	if opts.NoteDate != nil {
		noteDate = opts.NoteDate.UTC()
	}

	body := map[string]any{
		"user_id":      userID,
		"source_type":  sourceType,
		"note_date":    noteDate.Format(time.RFC3339),
		"title":        title,
		"content":      content,
		"preview":      preview,
		"updated_at":   time.Now().UTC().Format(time.RFC3339),
	}
	if opts.SourceID != nil {
		body["source_id"] = *opts.SourceID
	}

	if emb := n.noteEmbedding(ctx, title, content, preview); emb != nil {
		body["embedding"] = emb
	}

	var rows []Note
	if err := n.DB.Insert(ctx, "notes", body, &rows); err != nil {
		return Note{}, err
	}
	if len(rows) == 0 {
		return Note{}, fmt.Errorf("failed to create note")
	}
	return rows[0], nil
}

func (n *Notes) UpsertNoteFromSource(ctx context.Context, userID, sourceID, sourceType, content string, noteDate time.Time) (string, error) {
	title := noteTitle(content)
	preview := notePreview(content)
	now := time.Now().UTC().Format(time.RFC3339)

	body := map[string]any{
		"user_id":     userID,
		"source_id":   sourceID,
		"source_type": sourceType,
		"note_date":   noteDate.UTC().Format(time.RFC3339),
		"title":       title,
		"content":     content,
		"preview":     preview,
		"updated_at":  now,
	}

	if emb := n.noteEmbedding(ctx, title, content, preview); emb != nil {
		body["embedding"] = emb
	}

	var rows []struct {
		ID string `json:"id"`
	}
	if err := n.DB.Upsert(ctx, "notes", "source_id", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to upsert note from source")
	}
	return rows[0].ID, nil
}

// noteEmbedding returns an embedding for title + content (preview included for
// short notes), or nil if the embedder is unavailable or embedding fails.
func (n *Notes) noteEmbedding(ctx context.Context, title, content, preview string) []float32 {
	if n.Embedder == nil || !n.Embedder.Enabled() {
		return nil
	}
	text := strings.TrimSpace(title + "\n" + content)
	if text == "" {
		text = strings.TrimSpace(preview)
	}
	if text == "" {
		return nil
	}
	vec, err := n.Embedder.EmbedOne(ctx, text)
	if err != nil {
		return nil
	}
	return vec
}

func (n *Notes) GetNoteByID(ctx context.Context, userID, noteID string) (Note, error) {
	q := url.Values{}
	q.Set("select", n.selectColumns())
	q.Set("id", "eq."+noteID)
	q.Set("user_id", "eq."+userID)

	var rows []Note
	if err := n.DB.Get(ctx, "notes", q, &rows); err != nil {
		return Note{}, err
	}
	if len(rows) == 0 {
		return Note{}, fmt.Errorf("note not found")
	}
	return rows[0], nil
}

func (n *Notes) ListForDailyReview(ctx context.Context, userID string, limit int) ([]NoteSummary, error) {
	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("order", "note_date.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []NoteSummary
	if err := n.DB.Get(ctx, "notes", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (n *Notes) ListRecent(ctx context.Context, userID string, limit, offset int) ([]NoteSummary, error) {
	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("order", "note_date.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))

	var rows []NoteSummary
	if err := n.DB.Get(ctx, "notes", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (n *Notes) ListQuadrant(ctx context.Context, userID string, urgent, important bool, limit int) ([]NoteSummary, error) {
	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("is_urgent", fmt.Sprintf("eq.%t", urgent))
	q.Set("is_important", fmt.Sprintf("eq.%t", important))
	q.Set("order", "note_date.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []NoteSummary
	if err := n.DB.Get(ctx, "notes", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (n *Notes) SearchNotes(ctx context.Context, userID, query string, limit int) ([]NoteSummary, error) {
	terms := extractNoteSearchTerms(query)
	if len(terms) == 0 {
		return n.ListRecent(ctx, userID, limit, 0)
	}

	tsQuery := strings.Join(terms, " | ")
	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("search_vector", "fts.english.websearch."+tsQuery)
	q.Set("order", "note_date.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []NoteSummary
	if err := n.DB.Get(ctx, "notes", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

type noteSearchRow struct {
	Title   string `json:"title"`
	Preview string `json:"preview"`
	Content string `json:"content"`
}

func (n *Notes) RetrieveNoteSnippets(ctx context.Context, userID, transcript string, limit int) ([]string, error) {
	if !n.Enabled {
		return nil, nil
	}

	terms := extractNoteSearchTerms(transcript)
	var rows []noteSearchRow

	if len(terms) > 0 {
		tsQuery := strings.Join(terms, " | ")
		q := url.Values{}
		q.Set("select", "title,preview,content")
		q.Set("user_id", "eq."+userID)
		q.Set("search_vector", "fts.english.websearch."+tsQuery)
		q.Set("order", "note_date.desc")
		q.Set("limit", fmt.Sprintf("%d", limit*2))

		if err := n.DB.Get(ctx, "notes", q, &rows); err != nil {
			return nil, err
		}
	}

	if len(rows) == 0 {
		q := url.Values{}
		q.Set("select", "title,preview,content")
		q.Set("user_id", "eq."+userID)
		q.Set("order", "note_date.desc")
		q.Set("limit", fmt.Sprintf("%d", limit))

		if err := n.DB.Get(ctx, "notes", q, &rows); err != nil {
			return nil, err
		}
	}

	out := make([]string, 0, limit)
	seen := make(map[string]struct{})
	for _, row := range rows {
		snippet := formatNoteSnippet(row)
		key := strings.ToLower(snippet)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, snippet)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (n *Notes) UpdateNote(ctx context.Context, userID, noteID string, update NoteUpdate) (Note, error) {
	body := map[string]any{
		"updated_at":          time.Now().UTC().Format(time.RFC3339),
		"user_last_modified": time.Now().UTC().Format(time.RFC3339),
	}
	if update.Content != nil {
		content := strings.TrimSpace(*update.Content)
		body["content"] = content
		body["title"] = noteTitle(content)
		body["preview"] = notePreview(content)
	}
	if update.NoteDate != nil {
		body["note_date"] = *update.NoteDate
	}
	if update.IsImportant != nil {
		body["is_important"] = *update.IsImportant
	}
	if update.IsUrgent != nil {
		body["is_urgent"] = *update.IsUrgent
	}

	q := url.Values{}
	q.Set("id", "eq."+noteID)
	q.Set("user_id", "eq."+userID)

	if err := n.DB.Patch(ctx, "notes", q, body); err != nil {
		return Note{}, err
	}
	return n.GetNoteByID(ctx, userID, noteID)
}

func (n *Notes) UpdateNoteFlags(ctx context.Context, userID, noteID string, flags NoteFlags) (Note, error) {
	update := NoteUpdate{}
	if flags.IsImportant != nil {
		update.IsImportant = flags.IsImportant
	}
	if flags.IsUrgent != nil {
		update.IsUrgent = flags.IsUrgent
	}
	return n.UpdateNote(ctx, userID, noteID, update)
}

func (n *Notes) ApplyIndexerFlags(ctx context.Context, noteID string, urgent, important bool) error {
	q := url.Values{}
	q.Set("id", "eq."+noteID)
	q.Set("user_last_modified", "is.null")

	body := map[string]any{
		"is_urgent":    urgent,
		"is_important": important,
		"updated_at":   time.Now().UTC().Format(time.RFC3339),
	}
	return n.DB.Patch(ctx, "notes", q, body)
}

func (n *Notes) DeleteNote(ctx context.Context, userID, noteID string) error {
	q := url.Values{}
	q.Set("id", "eq."+noteID)
	q.Set("user_id", "eq."+userID)
	return n.DB.Delete(ctx, "notes", q)
}

func formatNoteSnippet(row noteSearchRow) string {
	if row.Title != "" && row.Preview != "" {
		return row.Title + ": " + row.Preview
	}
	if row.Title != "" {
		return row.Title
	}
	if row.Preview != "" {
		return row.Preview
	}
	text := strings.TrimSpace(row.Content)
	if len(text) > 120 {
		return text[:120] + "..."
	}
	return text
}

func extractNoteSearchTerms(transcript string) []string {
	return extractSearchTerms(transcript)
}

func noteTitle(content string) string {
	lines := nonEmptyNoteLines(content)
	title := "Untitled Note"
	if len(lines) > 0 {
		title = lines[0]
	}
	if len(title) > 50 {
		return title[:50] + "..."
	}
	return title
}

func notePreview(content string) string {
	lines := nonEmptyNoteLines(content)
	if len(lines) <= 1 {
		return ""
	}
	preview := strings.Join(lines[1:], " ")
	if len(preview) > 80 {
		return preview[:80] + "..."
	}
	return preview
}

func nonEmptyNoteLines(content string) []string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
