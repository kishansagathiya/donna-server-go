package account

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	exportSchema    = "donna-data-export-v1"
	exportVersion   = 1
	exportRateLimit = 15 * time.Minute
)

type Exporter struct {
	DB *storage.Supabase

	mu          sync.Mutex
	lastExports map[string]time.Time
}

type exportManifest struct {
	ExportVersion int    `json:"export_version"`
	ExportedAt    string `json:"exported_at"`
	UserID        string `json:"user_id"`
	Schema        string `json:"schema"`
	Counts        struct {
		Conversations int `json:"conversations"`
		Turns         int `json:"turns"`
		Sources       int `json:"sources"`
		Notes         int `json:"notes"`
		Facts         int `json:"facts"`
	} `json:"counts"`
	Integrity struct {
		Signature    *string `json:"signature"`
		SigningKeyID *string `json:"signing_key_id"`
	} `json:"integrity"`
}

type exportConversation struct {
	ID              string `json:"id"`
	UserID          string `json:"user_id"`
	Channel         string `json:"channel"`
	VoiceSessionID  string `json:"voice_session_id,omitempty"`
	ClientSessionID string `json:"client_session_id,omitempty"`
	EndedAt         string `json:"ended_at,omitempty"`
	CreatedAt       string `json:"created_at"`
	Turns           []exportTurn `json:"turns"`
}

type exportTurn struct {
	TurnIndex            int            `json:"turn_index"`
	UserTranscript       string         `json:"user_transcript"`
	AssistantTranscript  string         `json:"assistant_transcript"`
	UserAudioPath        string         `json:"user_audio_path,omitempty"`
	AssistantAudioPath   string         `json:"assistant_audio_path,omitempty"`
	UserAudioMime        string         `json:"user_audio_mime,omitempty"`
	AssistantAudioMime   string         `json:"assistant_audio_mime,omitempty"`
	Timings              map[string]any `json:"timings,omitempty"`
	CreatedAt            string         `json:"created_at"`
}

type exportFact struct {
	ID         string  `json:"id"`
	UserID     string  `json:"user_id"`
	Fact       string  `json:"fact"`
	EntityName *string `json:"entity_name"`
	Topic      *string `json:"topic"`
	SourceID   *string `json:"source_id"`
	Active     bool    `json:"active"`
	CreatedAt  string  `json:"created_at,omitempty"`
}

type exportNote struct {
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

const exportReadme = `Donna Data Export
=================

This ZIP archive contains a copy of your Donna data: conversations, notes,
memory facts, uploaded knowledge files, and account preferences.

Files:
  manifest.json       Export metadata and record counts
  profile.json        Your memory profile summary
  preferences.json    Account preferences (e.g. AI model)
  conversations/      Voice and text conversation transcripts (+ audio)
  knowledge/          Ingested sources, extracted facts, and original files
  notes.json          Your notes
  compile_log.json    Knowledge compilation history

Schema: donna-data-export-v1
`

func (e *Exporter) PreCheck(userID string) error {
	if e.DB == nil || !e.DB.Enabled() {
		return fmt.Errorf("data export unavailable")
	}
	if userID == "" {
		return fmt.Errorf("missing user id")
	}
	return e.checkRateLimit(userID)
}

func (e *Exporter) ExportUser(ctx context.Context, userID string, w io.Writer) error {
	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	manifest := exportManifest{
		ExportVersion: exportVersion,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		UserID:        userID,
		Schema:        exportSchema,
	}

	if err := e.writeProfile(ctx, userID, zipWriter); err != nil {
		return err
	}
	if err := e.writePreferences(ctx, userID, zipWriter); err != nil {
		return err
	}

	turnCount, err := e.writeConversations(ctx, userID, zipWriter, &manifest)
	if err != nil {
		return err
	}
	manifest.Counts.Turns = turnCount

	sourceCount, err := e.writeKnowledge(ctx, userID, zipWriter, &manifest)
	if err != nil {
		return err
	}
	manifest.Counts.Sources = sourceCount

	noteCount, err := e.writeNotes(ctx, userID, zipWriter, &manifest)
	if err != nil {
		return err
	}
	manifest.Counts.Notes = noteCount

	factCount, err := e.writeFacts(ctx, userID, zipWriter)
	if err != nil {
		return err
	}
	manifest.Counts.Facts = factCount

	if err := e.writeCompileLog(ctx, userID, zipWriter); err != nil {
		return err
	}
	if err := writeZipFile(zipWriter, "README.txt", []byte(exportReadme)); err != nil {
		return err
	}
	return writeZipJSON(zipWriter, "manifest.json", manifest)
}

func (e *Exporter) checkRateLimit(userID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastExports == nil {
		e.lastExports = make(map[string]time.Time)
	}
	if last, ok := e.lastExports[userID]; ok && time.Since(last) < exportRateLimit {
		return fmt.Errorf("export rate limited; try again later")
	}
	e.lastExports[userID] = time.Now()
	return nil
}

func (e *Exporter) writeProfile(ctx context.Context, userID string, zw *zip.Writer) error {
	q := url.Values{}
	q.Set("select", "user_id,summary,identity_facts,updated_at")
	q.Set("user_id", "eq."+userID)

	var rows []storage.UserProfile
	if err := e.DB.Get(ctx, "kb_user_profiles", q, &rows); err != nil {
		return fmt.Errorf("load profile: %w", err)
	}
	if len(rows) == 0 {
		rows = []storage.UserProfile{{UserID: userID, IdentityFacts: []string{}}}
	}
	return writeZipJSON(zw, "profile.json", rows[0])
}

func (e *Exporter) writePreferences(ctx context.Context, userID string, zw *zip.Writer) error {
	q := url.Values{}
	q.Set("select", "user_id,llm_model,updated_at")
	q.Set("user_id", "eq."+userID)

	var rows []map[string]any
	if err := e.DB.Get(ctx, "user_preferences", q, &rows); err != nil {
		return fmt.Errorf("load preferences: %w", err)
	}
	if len(rows) == 0 {
		rows = []map[string]any{{"user_id": userID}}
	}
	return writeZipJSON(zw, "preferences.json", rows[0])
}

func (e *Exporter) writeConversations(ctx context.Context, userID string, zw *zip.Writer, manifest *exportManifest) (int, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,channel,voice_session_id,client_session_id,ended_at,created_at")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "created_at.asc")

	var conversations []struct {
		ID              string  `json:"id"`
		UserID          string  `json:"user_id"`
		Channel         string  `json:"channel"`
		VoiceSessionID  *string `json:"voice_session_id"`
		ClientSessionID *string `json:"client_session_id"`
		EndedAt         *string `json:"ended_at"`
		CreatedAt       string  `json:"created_at"`
	}
	if err := e.DB.Get(ctx, "conversations", q, &conversations); err != nil {
		return 0, fmt.Errorf("load conversations: %w", err)
	}
	manifest.Counts.Conversations = len(conversations)

	turnCount := 0
	for _, conv := range conversations {
		turns, err := e.loadTurns(ctx, conv.ID)
		if err != nil {
			return turnCount, err
		}
		turnCount += len(turns)

		export := exportConversation{
			ID:        conv.ID,
			UserID:    conv.UserID,
			Channel:   conv.Channel,
			CreatedAt: conv.CreatedAt,
			Turns:     turns,
		}
		if conv.VoiceSessionID != nil {
			export.VoiceSessionID = *conv.VoiceSessionID
		}
		if conv.ClientSessionID != nil {
			export.ClientSessionID = *conv.ClientSessionID
		}
		if conv.EndedAt != nil {
			export.EndedAt = *conv.EndedAt
		}

		if err := writeZipJSON(zw, fmt.Sprintf("conversations/%s.json", conv.ID), export); err != nil {
			return turnCount, err
		}

		for _, turn := range turns {
			if err := e.writeTurnAudio(ctx, zw, conv.ID, turn); err != nil {
				log.Warn("export audio skipped", map[string]any{
					"conversationId": conv.ID,
					"turnIndex":      turn.TurnIndex,
					"error":          err.Error(),
				})
			}
		}
	}
	return turnCount, nil
}

func (e *Exporter) loadTurns(ctx context.Context, conversationID string) ([]exportTurn, error) {
	q := url.Values{}
	q.Set("select", "turn_index,user_transcript,assistant_transcript,user_audio_path,assistant_audio_path,user_audio_mime,assistant_audio_mime,timings,created_at")
	q.Set("conversation_id", "eq."+conversationID)
	q.Set("order", "turn_index.asc")

	var rows []exportTurn
	if err := e.DB.Get(ctx, "conversation_turns", q, &rows); err != nil {
		return nil, fmt.Errorf("load turns: %w", err)
	}
	return rows, nil
}

func (e *Exporter) writeTurnAudio(ctx context.Context, zw *zip.Writer, conversationID string, turn exportTurn) error {
	base := fmt.Sprintf("conversations/%s/audio/%d", conversationID, turn.TurnIndex)
	if turn.UserAudioPath != "" {
		data, err := e.DB.DownloadStorage(ctx, conversationAudioBucket, turn.UserAudioPath)
		if err != nil {
			return err
		}
		if err := writeZipFile(zw, base+"/user.wav", data); err != nil {
			return err
		}
	}
	if turn.AssistantAudioPath != "" {
		data, err := e.DB.DownloadStorage(ctx, conversationAudioBucket, turn.AssistantAudioPath)
		if err != nil {
			return err
		}
		ext := path.Ext(turn.AssistantAudioPath)
		if ext == "" {
			ext = ".wav"
		}
		if err := writeZipFile(zw, base+"/assistant"+ext, data); err != nil {
			return err
		}
	}
	return nil
}

func (e *Exporter) writeKnowledge(ctx context.Context, userID string, zw *zip.Writer, manifest *exportManifest) (int, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,source_type,content,conversation_id,turn_index,metadata,created_at")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "created_at.asc")

	var sources []storage.KbSource
	if err := e.DB.Get(ctx, "kb_sources", q, &sources); err != nil {
		return 0, fmt.Errorf("load sources: %w", err)
	}
	manifest.Counts.Sources = len(sources)

	if err := writeZipJSON(zw, "knowledge/sources.json", sources); err != nil {
		return 0, err
	}

	for _, source := range sources {
		storagePath, _ := source.Metadata["storage_path"].(string)
		storagePath = strings.TrimSpace(storagePath)
		if storagePath == "" {
			continue
		}
		data, err := e.DB.DownloadStorage(ctx, knowledgeAssetsBucket, storagePath)
		if err != nil {
			log.Warn("export asset skipped", map[string]any{
				"sourceId": source.ID,
				"path":     storagePath,
				"error":    err.Error(),
			})
			continue
		}
		filename := assetFilename(source)
		zipPath := fmt.Sprintf("knowledge/assets/%s/%s", source.ID, filename)
		if err := writeZipFile(zw, zipPath, data); err != nil {
			return len(sources), err
		}
	}
	return len(sources), nil
}

func assetFilename(source storage.KbSource) string {
	if name, ok := source.Metadata["original_filename"].(string); ok && strings.TrimSpace(name) != "" {
		return sanitizeFilename(name)
	}
	if title, ok := source.Metadata["title"].(string); ok && strings.TrimSpace(title) != "" {
		return sanitizeFilename(title)
	}
	return "asset"
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimSpace(name)
	if name == "" {
		return "asset"
	}
	return name
}

func (e *Exporter) writeFacts(ctx context.Context, userID string, zw *zip.Writer) (int, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,fact,entity_name,topic,source_id,active,created_at")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "created_at.asc")

	var facts []exportFact
	if err := e.DB.Get(ctx, "kb_facts", q, &facts); err != nil {
		return 0, fmt.Errorf("load facts: %w", err)
	}
	return len(facts), writeZipJSON(zw, "knowledge/facts.json", facts)
}

func (e *Exporter) writeNotes(ctx context.Context, userID string, zw *zip.Writer, manifest *exportManifest) (int, error) {
	q := url.Values{}
	q.Set("select", "id,user_id,source_id,source_type,note_date,title,content,preview,is_important,is_urgent,user_last_modified,created_at,updated_at")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "note_date.desc")

	var notes []exportNote
	if err := e.DB.Get(ctx, "notes", q, &notes); err != nil {
		return 0, fmt.Errorf("load notes: %w", err)
	}
	manifest.Counts.Notes = len(notes)
	return len(notes), writeZipJSON(zw, "notes.json", notes)
}

func (e *Exporter) writeCompileLog(ctx context.Context, userID string, zw *zip.Writer) error {
	q := url.Values{}
	q.Set("select", "id,user_id,conversation_id,status,turns_count,facts_added,error,supersede_misses,created_at,completed_at")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "created_at.asc")

	var rows []map[string]any
	if err := e.DB.Get(ctx, "kb_compile_log", q, &rows); err != nil {
		return fmt.Errorf("load compile log: %w", err)
	}
	return writeZipJSON(zw, "compile_log.json", rows)
}

func writeZipJSON(zw *zip.Writer, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeZipFile(zw, name, data)
}

func writeZipFile(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
