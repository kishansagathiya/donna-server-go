package granola

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

const (
	syncOverlap = 2 * time.Hour
	pageSize    = 25
)

type meetingMeta struct {
	ID        string
	Title     string
	Occurred  *time.Time
	Attendees []any
	Summary   string
	Raw       map[string]any
}

func (a *Adapter) RunSync(ctx context.Context, conn connectors.Connection, full bool) (connectors.SyncResult, error) {
	caller, err := a.OpenMCP(ctx, conn)
	if err != nil {
		return connectors.SyncResult{Status: "failed", Error: err.Error()}, err
	}
	defer caller.Close()

	// Refresh capabilities opportunistically.
	if names, err := caller.ListTools(ctx); err == nil {
		caps := capabilitiesFromTools(names)
		conn.Capabilities = caps
		capsJSON, _ := json.Marshal(caps)
		_ = a.Store.PatchConnection(ctx, conn.ID, map[string]any{"capabilities": json.RawMessage(capsJSON)})
	}

	meetings, err := a.listAllMeetings(ctx, caller, conn, full)
	if err != nil {
		if isAuthErr(err) {
			return connectors.SyncResult{Status: connectors.StatusReauthRequired, Error: err.Error()}, err
		}
		return connectors.SyncResult{Status: "failed", Error: err.Error()}, err
	}

	result := connectors.SyncResult{Status: "completed"}
	var meetingFailures int
	importedMeetings := conn.ImportedMeetingCount
	importedTranscripts := conn.ImportedTranscriptCount
	seen := 0

	for _, meta := range meetings {
		if err := ctx.Err(); err != nil {
			result.Status = "partial"
			result.Error = "sync cancelled"
			break
		}
		seen++
		if err := withRetry(ctx, 3, func() error {
			return a.upsertMeeting(ctx, caller, conn, meta, &result, &importedMeetings, &importedTranscripts)
		}); err != nil {
			if isAuthErr(err) {
				return connectors.SyncResult{Status: connectors.StatusReauthRequired, Error: err.Error()}, err
			}
			meetingFailures++
			log.Warn("granola meeting sync failed", map[string]any{
				"meetingId": meta.ID,
				"error":     err.Error(),
			})
			continue
		}
		// Page-level checkpoint in sync_cursor.
		if seen%pageSize == 0 {
			_ = a.Store.PatchConnection(ctx, conn.ID, map[string]any{
				"sync_cursor": map[string]any{
					"last_external_id": meta.ID,
					"seen":             seen,
					"full":             full,
				},
				"imported_meeting_count":    importedMeetings,
				"imported_transcript_count": importedTranscripts,
			})
		}
	}

	_ = a.Store.PatchConnection(ctx, conn.ID, map[string]any{
		"imported_meeting_count":    importedMeetings,
		"imported_transcript_count": importedTranscripts,
		"sync_cursor": map[string]any{
			"last_full":      full,
			"completed_at":   time.Now().UTC().Format(time.RFC3339),
			"meetings_seen":  seen,
			"overlap_window": syncOverlap.String(),
		},
	})

	if meetingFailures > 0 {
		result.Status = "partial"
		result.Error = fmt.Sprintf("%d meetings failed", meetingFailures)
	}
	return result, nil
}

func (a *Adapter) listAllMeetings(ctx context.Context, caller MCPCaller, conn connectors.Connection, full bool) ([]meetingMeta, error) {
	args := map[string]any{}
	if !full {
		// Incremental: short overlap window.
		since := time.Now().UTC().Add(-time.Hour - syncOverlap)
		if conn.LastSyncAt != nil {
			since = conn.LastSyncAt.Add(-syncOverlap)
		}
		args["updated_after"] = since.Format(time.RFC3339)
	}

	var raw string
	err := withRetry(ctx, 3, func() error {
		var callErr error
		raw, callErr = caller.CallTool(ctx, "list_meetings", args)
		return callErr
	})
	if err != nil {
		return nil, err
	}
	return parseMeetingList(raw), nil
}

func parseMeetingList(raw string) []meetingMeta {
	// Try common JSON shapes: {meetings:[...]}, [...], or newline text fallback.
	var asMap map[string]any
	if err := json.Unmarshal([]byte(raw), &asMap); err == nil {
		for _, key := range []string{"meetings", "notes", "items", "results"} {
			if arr, ok := asMap[key].([]any); ok {
				return parseMeetingArray(arr)
			}
		}
	}
	var asArr []any
	if err := json.Unmarshal([]byte(raw), &asArr); err == nil {
		return parseMeetingArray(asArr)
	}
	return nil
}

func parseMeetingArray(arr []any) []meetingMeta {
	out := make([]meetingMeta, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(m, "id", "meeting_id", "note_id")
		if id == "" {
			continue
		}
		title := stringField(m, "title", "name")
		var occurred *time.Time
		if ds := stringField(m, "date", "occurred_at", "created_at", "start_time"); ds != "" {
			if t, err := time.Parse(time.RFC3339, ds); err == nil {
				occurred = &t
			} else if t, err := time.Parse("2006-01-02", ds); err == nil {
				occurred = &t
			}
		}
		var attendees []any
		if a, ok := m["attendees"].([]any); ok {
			attendees = a
		}
		out = append(out, meetingMeta{
			ID:        id,
			Title:     title,
			Occurred:  occurred,
			Attendees: attendees,
			Raw:       m,
		})
	}
	return out
}

func (a *Adapter) upsertMeeting(
	ctx context.Context,
	caller MCPCaller,
	conn connectors.Connection,
	meta meetingMeta,
	result *connectors.SyncResult,
	importedMeetings, importedTranscripts *int,
) error {
	// Fetch full meeting notes when available.
	summary := meta.Summary
	title := meta.Title
	if conn.Capabilities.GetMeetings || true {
		raw, err := caller.CallTool(ctx, "get_meetings", map[string]any{
			"meeting_ids": []string{meta.ID},
		})
		if err == nil {
			detail := parseMeetingDetail(raw, meta.ID)
			if detail.Summary != "" {
				summary = detail.Summary
			}
			if detail.Title != "" {
				title = detail.Title
			}
			if detail.Occurred != nil {
				meta.Occurred = detail.Occurred
			}
			if len(detail.Attendees) > 0 {
				meta.Attendees = detail.Attendees
			}
		}
	}
	if strings.TrimSpace(summary) == "" {
		summary = title
	}
	if strings.TrimSpace(summary) == "" {
		summary = "Granola meeting " + meta.ID
	}

	summaryHash := connectors.ContentHash(summary)
	existing, err := a.Store.GetItem(ctx, conn.ID, meta.ID)
	if err != nil {
		return err
	}

	transcript := ""
	transcriptHash := ""
	if conn.Capabilities.Transcripts {
		raw, err := caller.CallTool(ctx, "get_meeting_transcript", map[string]any{
			"meeting_id": meta.ID,
		})
		if err == nil && strings.TrimSpace(raw) != "" {
			transcript = formatTranscript(raw)
			transcriptHash = connectors.ContentHash(transcript)
		}
		// Missing transcript capability / empty is not a failure.
	}

	unchanged := existing != nil && existing.SummaryHash == summaryHash && existing.TranscriptHash == transcriptHash
	if unchanged {
		return nil
	}

	noteID, kbMappings, err := a.replaceMeetingContent(ctx, conn, meta, title, summary, transcript, existing)
	if err != nil {
		return err
	}

	item := connectors.Item{
		ConnectionID:   conn.ID,
		UserID:         conn.UserID,
		Provider:       connectors.ProviderGranola,
		ExternalID:     meta.ID,
		Title:          title,
		OccurredAt:     meta.Occurred,
		Attendees:      meta.Attendees,
		SummaryHash:    summaryHash,
		TranscriptHash: transcriptHash,
		SummaryNoteID:  noteID,
		Metadata: map[string]any{
			"from_granola": true,
			"badge":        "From Granola",
		},
	}
	saved, err := a.Store.UpsertItem(ctx, item)
	if err != nil {
		return err
	}
	if err := a.Store.ReplaceItemSources(ctx, saved.ID, conn.UserID, kbMappings); err != nil {
		return err
	}

	result.MeetingsUpserted++
	if existing == nil {
		*importedMeetings++
	}
	if transcript != "" && (existing == nil || existing.TranscriptHash != transcriptHash) {
		result.TranscriptsSaved++
		if existing == nil || existing.TranscriptHash == "" {
			*importedTranscripts++
		}
	}
	return nil
}

func (a *Adapter) replaceMeetingContent(
	ctx context.Context,
	conn connectors.Connection,
	meta meetingMeta,
	title, summary, transcript string,
	existing *connectors.Item,
) (noteID string, mappings []connectors.ItemSourceMapping, err error) {
	// Delete prior kb_sources for this item (transactional enough for PostgREST: delete then insert).
	if existing != nil {
		oldIDs, _ := a.Store.ListItemSourceIDs(ctx, existing.ID)
		for _, id := range oldIDs {
			q := url.Values{}
			q.Set("id", "eq."+id)
			q.Set("user_id", "eq."+conn.UserID)
			_ = a.Store.DB.Delete(ctx, "kb_sources", q)
		}
		if existing.SummaryNoteID != "" {
			// Update in place via upsert-by-source below using same note if possible.
			noteID = existing.SummaryNoteID
		}
	}

	noteDate := time.Now().UTC()
	if meta.Occurred != nil {
		noteDate = meta.Occurred.UTC()
	}
	noteBody := formatSummaryNote(title, summary, meta)
	noteID, err = a.upsertIntegrationNote(ctx, conn.UserID, noteID, meta.ID, noteBody, noteDate, title)
	if err != nil {
		return "", nil, err
	}

	// Summary chunk in kb_sources for retrieval (not compiled into personal facts).
	summarySourceID, err := a.insertIntegrationSource(ctx, conn.UserID, noteBody, map[string]any{
		"provider":    connectors.ProviderGranola,
		"meeting_id":  meta.ID,
		"kind":        "summary",
		"from_granola": true,
		"title":       title,
	})
	if err != nil {
		return "", nil, err
	}
	mappings = append(mappings, connectors.ItemSourceMapping{
		KbSourceID: summarySourceID,
		Kind:       "summary",
		ChunkIndex: 0,
	})

	if transcript != "" {
		chunks := connectors.ChunkTranscript(transcript)
		for i, chunk := range chunks {
			id, err := a.insertIntegrationSource(ctx, conn.UserID, chunk, map[string]any{
				"provider":     connectors.ProviderGranola,
				"meeting_id":   meta.ID,
				"kind":         "transcript_chunk",
				"chunk_index":  i,
				"from_granola": true,
				"title":        title,
			})
			if err != nil {
				return "", nil, err
			}
			mappings = append(mappings, connectors.ItemSourceMapping{
				KbSourceID: id,
				Kind:       "transcript_chunk",
				ChunkIndex: i,
			})
		}
	}
	return noteID, mappings, nil
}

func formatSummaryNote(title, summary string, meta meetingMeta) string {
	var b strings.Builder
	if title != "" {
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(summary))
	b.WriteString("\n\n")
	b.WriteString("— From Granola")
	if meta.Occurred != nil {
		b.WriteString(" · ")
		b.WriteString(meta.Occurred.Format("2006-01-02"))
	}
	return b.String()
}

func formatTranscript(raw string) string {
	raw = strings.TrimSpace(raw)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		if t, ok := parsed["transcript"].(string); ok {
			return strings.TrimSpace(t)
		}
		if segments, ok := parsed["segments"].([]any); ok {
			var lines []string
			for _, seg := range segments {
				m, ok := seg.(map[string]any)
				if !ok {
					continue
				}
				speaker := stringField(m, "speaker", "speaker_name", "name")
				text := stringField(m, "text", "content")
				if text == "" {
					continue
				}
				if speaker != "" {
					lines = append(lines, speaker+": "+text)
				} else {
					lines = append(lines, text)
				}
			}
			if len(lines) > 0 {
				return strings.Join(lines, "\n")
			}
		}
	}
	return raw
}

func parseMeetingDetail(raw, id string) meetingMeta {
	list := parseMeetingList(raw)
	for _, m := range list {
		if m.ID == id || id == "" {
			if m.Summary == "" {
				m.Summary = stringField(m.Raw, "notes", "summary", "private_notes", "content")
			}
			return m
		}
	}
	// Single object
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		return meetingMeta{
			ID:      id,
			Title:   stringField(m, "title", "name"),
			Summary: stringField(m, "notes", "summary", "private_notes", "content"),
			Raw:     m,
		}
	}
	return meetingMeta{ID: id, Summary: strings.TrimSpace(raw)}
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return strings.TrimSpace(t)
				}
			case float64:
				return fmt.Sprintf("%.0f", t)
			}
		}
	}
	return ""
}

func isAuthErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "reauth")
}

func (a *Adapter) upsertIntegrationNote(ctx context.Context, userID, noteID, externalID, content string, noteDate time.Time, title string) (string, error) {
	if a.Notes == nil || !a.Notes.Enabled {
		return "", fmt.Errorf("notes store unavailable")
	}
	body := map[string]any{
		"title":       title,
		"content":     content,
		"preview":     preview(content),
		"note_date":   noteDate.Format(time.RFC3339),
		"source_type": "integration",
		"updated_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if emb := a.noteEmbedding(ctx, title, content); emb != nil {
		body["embedding"] = emb
	}
	// Prefer updating the existing summary note tracked by integration_items.
	// notes.source_id is a UUID FK to kb_sources — do not store a string meeting id there.
	if noteID != "" {
		q := url.Values{}
		q.Set("id", "eq."+noteID)
		q.Set("user_id", "eq."+userID)
		if err := a.Notes.DB.Patch(ctx, "notes", q, body); err == nil {
			return noteID, nil
		}
	}
	body["user_id"] = userID
	var rows []struct {
		ID string `json:"id"`
	}
	if err := a.Notes.DB.Insert(ctx, "notes", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to insert integration note")
	}
	return rows[0].ID, nil
}

func (a *Adapter) insertIntegrationSource(ctx context.Context, userID, content string, metadata map[string]any) (string, error) {
	if a.KB == nil || !a.KB.Enabled {
		return "", fmt.Errorf("knowledge store unavailable")
	}
	body := map[string]any{
		"user_id":     userID,
		"source_type": "integration",
		"content":     content,
		"metadata":    metadata,
	}
	if a.KB.Embedder != nil && a.KB.Embedder.Enabled() {
		if vec, err := a.KB.Embedder.EmbedOne(ctx, content); err == nil {
			body["embedding"] = vec
		}
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := a.KB.DB.Insert(ctx, "kb_sources", body, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("failed to insert integration source")
	}
	return rows[0].ID, nil
}

func (a *Adapter) noteEmbedding(ctx context.Context, title, content string) []float32 {
	if a.Notes == nil || a.Notes.Embedder == nil || !a.Notes.Embedder.Enabled() {
		return nil
	}
	text := strings.TrimSpace(title + "\n" + content)
	if text == "" {
		return nil
	}
	vec, err := a.Notes.Embedder.EmbedOne(ctx, text)
	if err != nil {
		return nil
	}
	return vec
}

func preview(content string) string {
	content = strings.TrimSpace(content)
	r := []rune(content)
	if len(r) <= 180 {
		return content
	}
	return string(r[:180]) + "…"
}
