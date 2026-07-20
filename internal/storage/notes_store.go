package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

// NoteAudioBucket is the Supabase storage bucket where dictated note audio
// (16 kHz mono 16-bit WAV, RIFF-wrapped by the voice session) is uploaded.
const NoteAudioBucket = "note-audio"

type Note struct {
	ID               string   `json:"id"`
	UserID           string   `json:"user_id"`
	SourceID         *string  `json:"source_id"`
	SourceType       string   `json:"source_type"`
	NoteDate         string   `json:"note_date"`
	Title            string   `json:"title"`
	Content          string   `json:"content"`
	Preview          string   `json:"preview"`
	IsImportant      bool     `json:"is_important"`
	IsUrgent         bool     `json:"is_urgent"`
	Keywords         []string `json:"keywords"`
	Category         *string  `json:"category"`
	UserLastModified *string  `json:"user_last_modified"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	ContentVersion   int64    `json:"content_version"`
	EnrichmentStatus string   `json:"enrichment_status"`
	EnrichmentVersion int64   `json:"enrichment_version"`
	AudioPath        *string  `json:"audio_path,omitempty"`
	AudioMime        *string  `json:"audio_mime,omitempty"`
	// AudioURL is populated only on read by the notes handler (signed Supabase
	// URL); it is never persisted and is empty on inserts/updates. Omitted from
	// JSON when empty so the field disappears for notes without audio.
	AudioURL string `json:"audio_url,omitempty"`
}

// HasAudio reports whether this note has a stored dictation we can play back.
// Only notes whose source_type is "manual" (committed by the /voice `notes`
// mode flow) ever have AudioPath set.
func (n Note) HasAudio() bool {
	return n.AudioPath != nil && strings.TrimSpace(*n.AudioPath) != ""
}

type NoteSummary struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Preview     string   `json:"preview"`
	NoteDate    string   `json:"note_date"`
	IsImportant bool     `json:"is_important"`
	IsUrgent    bool     `json:"is_urgent"`
	SourceType  string   `json:"source_type"`
	Keywords    []string `json:"keywords"`
	Category    *string  `json:"category"`
	HasAudio    bool     `json:"has_audio"`
}

type NoteFlags struct {
	IsImportant *bool
	IsUrgent    *bool
}

// NoteAudioInput carries the dictation WAV bytes (RIFF-wrapped PCM16) to Attach
// to a note. Mime defaults to audio/wav. Used only by the /voice `notes` mode
// flow right now — text-created notes pass nil.
type NoteAudioInput struct {
	WAV  []byte
	Mime string
}

// noteSummaryRow decodes the audio_path column from PostgREST and then folds
// it into NoteSummary.HasAudio. We can't add audio_path to NoteSummary itself
// (it'd leak the storage path to clients), so we decode into this wrapper.
type noteSummaryRow struct {
	NoteSummary
	AudioPath *string `json:"audio_path"`
}

func (r noteSummaryRow) toSummary() NoteSummary {
	s := r.NoteSummary
	s.HasAudio = r.AudioPath != nil && strings.TrimSpace(*r.AudioPath) != ""
	return s
}

type NoteUpdate struct {
	Content     *string
	NoteDate    *string
	IsImportant *bool
	IsUrgent    *bool
}

type Notes struct {
	DB       *Supabase
	Enabled  bool
	Embedder Embedder
}

func (n *Notes) selectColumns() string {
	return "id,user_id,source_id,source_type,note_date,title,content,preview,is_important,is_urgent,keywords,category,user_last_modified,created_at,updated_at,audio_path,audio_mime"
}

func (n *Notes) summaryColumns() string {
	return "id,title,preview,note_date,is_important,is_urgent,source_type,keywords,category,audio_path"
}

// CreateNoteOptions configures a freshly-created note. Audio, when non-nil,
// is uploaded to the note-audio bucket before the row is inserted so the stored
// audio_path is consistent from the start. SourceID/NoteDate behave as before.
type CreateNoteOptions struct {
	SourceID *string
	NoteDate *time.Time
	Audio    *NoteAudioInput
}

func (n *Notes) CreateNote(ctx context.Context, userID, sourceType, content string, opts CreateNoteOptions) (Note, error) {
	title := noteTitle(content)
	preview := notePreview(content)
	noteDate := time.Now().UTC()
	if opts.NoteDate != nil {
		noteDate = opts.NoteDate.UTC()
	}

	body := map[string]any{
		"user_id":     userID,
		"source_type": sourceType,
		"note_date":   noteDate.Format(time.RFC3339),
		"title":       title,
		"content":     content,
		"preview":     preview,
		"updated_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if opts.SourceID != nil {
		body["source_id"] = *opts.SourceID
	}

	// Attach dictation audio, if provided. We upload first and only persist the
	// path on success; an upload failure downgrades to a text-only note rather
	// than failing the whole turn (the transcript is the source of truth).
	if opts.Audio != nil && len(opts.Audio.WAV) > 0 {
		mime := strings.TrimSpace(opts.Audio.Mime)
		if mime == "" {
			mime = "audio/wav"
		}
		ext := "wav"
		if strings.Contains(mime, "mpeg") {
			ext = "mp3"
		}
		objectPath := fmt.Sprintf("%s/%s.%s", userID, uuid.NewString(), ext)
		if err := n.DB.UploadStorage(ctx, NoteAudioBucket, objectPath, mime, opts.Audio.WAV); err != nil {
			log.Warn("note audio upload failed — saving transcript only", map[string]any{
				"userId": log.ShortID(userID),
				"error":  err.Error(),
			})
		} else {
			body["audio_path"] = objectPath
			body["audio_mime"] = mime
		}
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

// SignedAudioURL returns a short-lived signed download URL for the note's
// stored dictation, or "" when the note has no audio (typed/manual callers,
// or older dictated notes pre-migration). Errors during signing are logged
// and treated as no-audio so callers fall back to text-only gracefully.
const noteAudioSignedURLTTL = 30 * time.Minute

func (n *Notes) SignedAudioURL(ctx context.Context, note Note) string {
	if !note.HasAudio() || n.DB == nil || !n.DB.Enabled() {
		return ""
	}
	signed, err := n.DB.CreateSignedURL(ctx, NoteAudioBucket, *note.AudioPath, noteAudioSignedURLTTL)
	if err != nil {
		log.Warn("failed to sign note audio url", map[string]any{
			"noteId":    note.ID,
			"audioPath": *note.AudioPath,
			"error":     err.Error(),
		})
		return ""
	}
	return signed
}

func (n *Notes) ListForDailyReview(ctx context.Context, userID string, limit int) ([]NoteSummary, error) {
	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("order", "note_date.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var raw []noteSummaryRow
	if err := n.DB.Get(ctx, "notes", q, &raw); err != nil {
		return nil, err
	}
	out := make([]NoteSummary, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.toSummary())
	}
	return out, nil
}

func (n *Notes) ListRecent(ctx context.Context, userID string, limit, offset int) ([]NoteSummary, error) {
	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("order", "note_date.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))

	var raw []noteSummaryRow
	if err := n.DB.Get(ctx, "notes", q, &raw); err != nil {
		return nil, err
	}
	out := make([]NoteSummary, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.toSummary())
	}
	return out, nil
}

func (n *Notes) GetNotesByIDs(ctx context.Context, userID string, noteIDs []string, limit int) ([]NoteSummary, error) {
	if len(noteIDs) == 0 {
		return nil, nil
	}
	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("id", "in."+strings.Join(noteIDs, ","))
	q.Set("order", "note_date.desc")
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}

	var raw []noteSummaryRow
	if err := n.DB.Get(ctx, "notes", q, &raw); err != nil {
		return nil, err
	}
	out := make([]NoteSummary, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.toSummary())
	}
	return out, nil
}

func (n *Notes) ListQuadrant(ctx context.Context, userID string, urgent, important bool, limit int) ([]NoteSummary, error) {
	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("is_urgent", fmt.Sprintf("eq.%t", urgent))
	q.Set("is_important", fmt.Sprintf("eq.%t", important))
	q.Set("order", "note_date.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var raw []noteSummaryRow
	if err := n.DB.Get(ctx, "notes", q, &raw); err != nil {
		return nil, err
	}
	out := make([]NoteSummary, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.toSummary())
	}
	return out, nil
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

	var raw []noteSummaryRow
	if err := n.DB.Get(ctx, "notes", q, &raw); err != nil {
		return nil, err
	}
	out := make([]NoteSummary, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.toSummary())
	}
	return out, nil
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
		"updated_at":         time.Now().UTC().Format(time.RFC3339),
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
	return n.ApplyIndexerMeta(ctx, noteID, urgent, important, nil, "")
}

// ApplyIndexerMeta persists the indexer's flags + keywords/category for a note,
// but only when the user has not manually edited it (user_last_modified is null).
func (n *Notes) ApplyIndexerMeta(ctx context.Context, noteID string, urgent, important bool, keywords []string, category string) error {
	q := url.Values{}
	q.Set("id", "eq."+noteID)
	q.Set("user_last_modified", "is.null")

	body := map[string]any{
		"is_urgent":    urgent,
		"is_important": important,
		"updated_at":   time.Now().UTC().Format(time.RFC3339),
	}
	if keywords != nil {
		body["keywords"] = keywords
	}
	if category != "" {
		body["category"] = category
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
