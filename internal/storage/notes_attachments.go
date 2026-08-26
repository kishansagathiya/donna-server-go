package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

const (
	NoteAttachmentsBucket     = "note-attachments"
	noteAttachmentSignedTTL   = 30 * time.Minute
	MaxNoteAttachments        = 10
	MaxNoteAttachmentBytes    = 15 * 1024 * 1024
)

// SaveNoteAttachment is an in-memory image to upload with a note.
type SaveNoteAttachment struct {
	Filename string
	Mime     string
	Data     []byte
}

// noteAttachmentRow is the persisted JSON shape for notes.attachments.
type noteAttachmentRow struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Filename    string `json:"filename"`
	Mime        string `json:"mime,omitempty"`
	StoragePath string `json:"storage_path,omitempty"`
}

// NoteAttachment is returned to clients (storage paths never leave the server).
type NoteAttachment struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	Mime     string `json:"mime,omitempty"`
	URL      string `json:"url,omitempty"`
}

func parseNoteAttachmentRows(raw json.RawMessage) []noteAttachmentRow {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var rows []noteAttachmentRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil
	}
	return rows
}

func (n *Notes) signNoteAttachments(ctx context.Context, raw json.RawMessage) []NoteAttachment {
	rows := parseNoteAttachmentRows(raw)
	if len(rows) == 0 {
		return nil
	}
	out := make([]NoteAttachment, 0, len(rows))
	for _, row := range rows {
		att := NoteAttachment{
			ID:       row.ID,
			Kind:     row.Kind,
			Filename: row.Filename,
			Mime:     row.Mime,
		}
		if att.Kind == "" {
			att.Kind = "file"
		}
		if path := strings.TrimSpace(row.StoragePath); path != "" && n != nil && n.DB != nil && n.DB.Enabled() {
			if signed, err := n.DB.CreateSignedURL(ctx, NoteAttachmentsBucket, path, noteAttachmentSignedTTL); err == nil {
				att.URL = signed
			} else {
				log.Warn("note attachment sign failed", map[string]any{
					"path":  path,
					"error": err.Error(),
				})
			}
		}
		out = append(out, att)
	}
	return out
}

func (n *Notes) firstSignedImageURL(ctx context.Context, raw json.RawMessage) string {
	for _, att := range n.signNoteAttachments(ctx, raw) {
		if strings.TrimSpace(att.URL) != "" {
			return att.URL
		}
	}
	return ""
}

func (n *Notes) applyNoteAttachments(ctx context.Context, note *Note, raw json.RawMessage) {
	if note == nil {
		return
	}
	note.Attachments = n.signNoteAttachments(ctx, raw)
	note.HasImage = len(parseNoteAttachmentRows(raw)) > 0
}

func (n *Notes) applySummaryAttachments(ctx context.Context, summary *NoteSummary, raw json.RawMessage) {
	if summary == nil {
		return
	}
	rows := parseNoteAttachmentRows(raw)
	summary.HasImage = len(rows) > 0
	if summary.HasImage {
		summary.ImageURL = n.firstSignedImageURL(ctx, raw)
	}
}

func (n *Notes) uploadNoteAttachments(ctx context.Context, userID, noteID string, existing []noteAttachmentRow, incoming []SaveNoteAttachment) ([]noteAttachmentRow, error) {
	if len(incoming) == 0 {
		return existing, nil
	}
	if len(existing)+len(incoming) > MaxNoteAttachments {
		return nil, fmt.Errorf("too many attachments (max %d)", MaxNoteAttachments)
	}
	out := append([]noteAttachmentRow(nil), existing...)
	uploaded := make([]string, 0, len(incoming))
	for _, att := range incoming {
		if len(att.Data) == 0 {
			return nil, fmt.Errorf("empty file")
		}
		if len(att.Data) > MaxNoteAttachmentBytes {
			return nil, fmt.Errorf("file too large (max 15MB)")
		}
		filename := strings.TrimSpace(att.Filename)
		if filename == "" {
			filename = "photo.jpg"
		}
		mime := strings.TrimSpace(att.Mime)
		if mime == "" {
			mime = "image/jpeg"
		}
		id := uuid.NewString()
		objectPath := noteAttachmentObjectPath(userID, noteID, id, filename, mime)
		if n.DB == nil || !n.DB.Enabled() {
			return nil, fmt.Errorf("storage unavailable")
		}
		if err := n.DB.UploadStorage(ctx, NoteAttachmentsBucket, objectPath, mime, att.Data); err != nil {
			_ = n.DB.DeleteStorageObjects(ctx, NoteAttachmentsBucket, uploaded)
			return nil, fmt.Errorf("upload %s: %w", filename, err)
		}
		uploaded = append(uploaded, objectPath)
		out = append(out, noteAttachmentRow{
			ID:          id,
			Kind:        "file",
			Filename:    filename,
			Mime:        mime,
			StoragePath: objectPath,
		})
	}
	return out, nil
}

func removeNoteAttachmentRows(existing []noteAttachmentRow, removeIDs []string) (kept []noteAttachmentRow, removedPaths []string) {
	if len(removeIDs) == 0 {
		return existing, nil
	}
	drop := make(map[string]struct{}, len(removeIDs))
	for _, id := range removeIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			drop[id] = struct{}{}
		}
	}
	kept = make([]noteAttachmentRow, 0, len(existing))
	for _, row := range existing {
		if _, ok := drop[row.ID]; ok {
			if path := strings.TrimSpace(row.StoragePath); path != "" {
				removedPaths = append(removedPaths, path)
			}
			continue
		}
		kept = append(kept, row)
	}
	return kept, removedPaths
}

func noteAttachmentObjectPath(userID, noteID, attID, filename, mime string) string {
	safe := sanitizeNoteAttachmentFilename(filename)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(safe), "."))
	if ext == "" {
		ext = noteAttachmentExtForMime(mime)
	}
	if ext != "" && !strings.HasSuffix(strings.ToLower(safe), "."+ext) {
		safe = safe + "." + ext
	}
	return path.Join(userID, noteID, attID+"_"+safe)
}

func sanitizeNoteAttachmentFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "photo"
	}
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "photo"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func noteAttachmentExtForMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/heic":
		return "heic"
	case "image/heif":
		return "heif"
	default:
		return "jpg"
	}
}

func attachmentStoragePaths(rows []noteAttachmentRow) []string {
	var paths []string
	for _, row := range rows {
		if p := strings.TrimSpace(row.StoragePath); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func (n *Notes) summariesFromRaw(ctx context.Context, raw []noteSummaryRow) []NoteSummary {
	out := make([]NoteSummary, 0, len(raw))
	for _, r := range raw {
		s := r.toSummary()
		n.applySummaryAttachments(ctx, &s, r.AttachmentsRaw)
		out = append(out, s)
	}
	return out
}

func (n *Notes) loadAttachmentRows(ctx context.Context, userID, noteID string) ([]noteAttachmentRow, error) {
	q := url.Values{}
	q.Set("select", "attachments")
	q.Set("id", "eq."+noteID)
	q.Set("user_id", "eq."+userID)
	var rows []struct {
		Attachments json.RawMessage `json:"attachments"`
	}
	if err := n.DB.Get(ctx, "notes", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("note not found")
	}
	return parseNoteAttachmentRows(rows[0].Attachments), nil
}
