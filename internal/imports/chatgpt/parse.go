package chatgpt

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

var conversationsFileRe = regexp.MustCompile(`(?i)^conversations(-\d+)?\.json$`)

// Conversation is a flattened ChatGPT conversation ready for import.
type Conversation struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Messages  []Message
}

// Message is one turn on the current-node path.
type Message struct {
	Role    string
	Content string
	At      time.Time
}

// MemoryEntry is a ChatGPT saved memory from memory.json.
type MemoryEntry struct {
	ID      string
	Content string
	Updated time.Time
}

// ExportPayload is the parsed contents of a ChatGPT data export ZIP.
type ExportPayload struct {
	Conversations []Conversation
	Memories      []MemoryEntry
}

type rawConversation struct {
	ID             string                     `json:"id"`
	ConversationID string                     `json:"conversation_id"`
	Title          string                     `json:"title"`
	CreateTime     *float64                   `json:"create_time"`
	UpdateTime     *float64                   `json:"update_time"`
	CurrentNode    string                     `json:"current_node"`
	Mapping        map[string]rawMappingNode  `json:"mapping"`
}

type rawMappingNode struct {
	ID       string   `json:"id"`
	Parent   *string  `json:"parent"`
	Children []string `json:"children"`
	Message  *rawMessage `json:"message"`
}

type rawMessage struct {
	ID         string          `json:"id"`
	Author     rawAuthor       `json:"author"`
	CreateTime *float64        `json:"create_time"`
	Content    json.RawMessage `json:"content"`
}

type rawAuthor struct {
	Role string `json:"role"`
}

// ParseExportZIP reads a ChatGPT export archive and returns conversations + memories.
func ParseExportZIP(data []byte) (ExportPayload, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ExportPayload{}, fmt.Errorf("invalid zip: %w", err)
	}

	var convFiles []*zip.File
	var memoryFile *zip.File
	for _, f := range zr.File {
		base := path.Base(f.Name)
		if strings.HasPrefix(base, ".") || strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}
		if conversationsFileRe.MatchString(base) {
			convFiles = append(convFiles, f)
			continue
		}
		if strings.EqualFold(base, "memory.json") {
			memoryFile = f
		}
	}
	if len(convFiles) == 0 {
		return ExportPayload{}, fmt.Errorf("conversations.json not found in export zip")
	}

	sort.Slice(convFiles, func(i, j int) bool {
		return convFiles[i].Name < convFiles[j].Name
	})

	var conversations []Conversation
	for _, f := range convFiles {
		raw, err := readZipFile(f)
		if err != nil {
			return ExportPayload{}, fmt.Errorf("read %s: %w", f.Name, err)
		}
		parsed, err := parseConversationsJSON(raw)
		if err != nil {
			return ExportPayload{}, fmt.Errorf("parse %s: %w", f.Name, err)
		}
		conversations = append(conversations, parsed...)
	}

	var memories []MemoryEntry
	if memoryFile != nil {
		raw, err := readZipFile(memoryFile)
		if err != nil {
			return ExportPayload{}, fmt.Errorf("read memory.json: %w", err)
		}
		memories, err = parseMemoryJSON(raw)
		if err != nil {
			return ExportPayload{}, fmt.Errorf("parse memory.json: %w", err)
		}
	}

	return ExportPayload{
		Conversations: conversations,
		Memories:      memories,
	}, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func parseConversationsJSON(raw []byte) ([]Conversation, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}

	var list []rawConversation
	if err := json.Unmarshal(raw, &list); err != nil {
		// Some exports wrap the array.
		var wrapped struct {
			Conversations []rawConversation `json:"conversations"`
		}
		if err2 := json.Unmarshal(raw, &wrapped); err2 != nil {
			return nil, err
		}
		list = wrapped.Conversations
	}

	out := make([]Conversation, 0, len(list))
	for _, c := range list {
		flat := flattenConversation(c)
		if flat.ID == "" || len(flat.Messages) == 0 {
			continue
		}
		out = append(out, flat)
	}
	return out, nil
}

// FlattenConversationPath reconstructs the user-visible thread by walking
// current_node → parent until the synthetic root.
func FlattenConversationPath(currentNode string, mapping map[string]rawMappingNode) []Message {
	if currentNode == "" || len(mapping) == 0 {
		return nil
	}
	var chain []Message
	seen := map[string]struct{}{}
	id := currentNode
	for id != "" {
		if _, ok := seen[id]; ok {
			break
		}
		seen[id] = struct{}{}
		node, ok := mapping[id]
		if !ok {
			break
		}
		if node.Message != nil {
			role := strings.ToLower(strings.TrimSpace(node.Message.Author.Role))
			text := extractMessageText(node.Message.Content)
			if text != "" && (role == "user" || role == "assistant") {
				chain = append(chain, Message{
					Role:    role,
					Content: text,
					At:      unixFloat(node.Message.CreateTime),
				})
			}
		}
		if node.Parent == nil || strings.TrimSpace(*node.Parent) == "" {
			break
		}
		id = *node.Parent
	}
	// chain is leaf→root; reverse to chronological.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func flattenConversation(c rawConversation) Conversation {
	id := strings.TrimSpace(c.ConversationID)
	if id == "" {
		id = strings.TrimSpace(c.ID)
	}
	title := strings.TrimSpace(c.Title)
	if title == "" {
		title = "ChatGPT conversation"
	}
	msgs := FlattenConversationPath(c.CurrentNode, c.Mapping)
	return Conversation{
		ID:        id,
		Title:     title,
		CreatedAt: unixFloat(c.CreateTime),
		UpdatedAt: unixFloat(c.UpdateTime),
		Messages:  msgs,
	}
}

func extractMessageText(content json.RawMessage) string {
	content = bytes.TrimSpace(content)
	if len(content) == 0 || string(content) == "null" {
		return ""
	}
	// Common shape: {"content_type":"text","parts":["..."]}
	var obj struct {
		ContentType string `json:"content_type"`
		Parts       []any  `json:"parts"`
		Text        string `json:"text"`
		Result      string `json:"result"`
	}
	if err := json.Unmarshal(content, &obj); err == nil {
		var parts []string
		for _, p := range obj.Parts {
			switch t := p.(type) {
			case string:
				if s := strings.TrimSpace(t); s != "" {
					parts = append(parts, s)
				}
			case map[string]any:
				if s, ok := t["text"].(string); ok {
					if s = strings.TrimSpace(s); s != "" {
						parts = append(parts, s)
					}
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		if s := strings.TrimSpace(obj.Text); s != "" {
			return s
		}
		if s := strings.TrimSpace(obj.Result); s != "" {
			return s
		}
	}
	// Plain string content
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func parseMemoryJSON(raw []byte) ([]MemoryEntry, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}

	tryList := func(list []map[string]any) []MemoryEntry {
		out := make([]MemoryEntry, 0, len(list))
		for _, m := range list {
			content := firstString(m, "content", "memory", "text", "value")
			if content == "" {
				continue
			}
			id := firstString(m, "id", "memory_id")
			updated := time.Time{}
			if v, ok := m["updated_at"]; ok {
				updated = coerceTime(v)
			} else if v, ok := m["update_time"]; ok {
				updated = coerceTime(v)
			}
			out = append(out, MemoryEntry{ID: id, Content: content, Updated: updated})
		}
		return out
	}

	var asList []map[string]any
	if err := json.Unmarshal(raw, &asList); err == nil {
		return tryList(asList), nil
	}

	var wrapped map[string]any
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	for _, key := range []string{"memories", "memory", "items", "data"} {
		if v, ok := wrapped[key]; ok {
			b, _ := json.Marshal(v)
			var list []map[string]any
			if err := json.Unmarshal(b, &list); err == nil {
				return tryList(list), nil
			}
		}
	}
	// Single object
	if content := firstString(wrapped, "content", "memory", "text"); content != "" {
		return []MemoryEntry{{
			ID:      firstString(wrapped, "id"),
			Content: content,
			Updated: coerceTime(wrapped["updated_at"]),
		}}, nil
	}
	return nil, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if s := strings.TrimSpace(t); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func coerceTime(v any) time.Time {
	switch t := v.(type) {
	case string:
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			return ts
		}
		if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return ts
		}
	case float64:
		f := t
		return unixFloat(&f)
	case json.Number:
		f, err := t.Float64()
		if err == nil {
			return unixFloat(&f)
		}
	}
	return time.Time{}
}

func unixFloat(v *float64) time.Time {
	if v == nil || *v <= 0 {
		return time.Time{}
	}
	sec := int64(*v)
	nsec := int64((*v - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC()
}

// FormatConversationNote builds note/source content for a flattened conversation.
func FormatConversationNote(c Conversation) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(c.Title)
	b.WriteString("\n\n")
	for _, m := range c.Messages {
		label := "Assistant"
		if m.Role == "user" {
			label = "User"
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	b.WriteString("— From ChatGPT export")
	if !c.CreatedAt.IsZero() {
		b.WriteString(" · ")
		b.WriteString(c.CreatedAt.Format("2006-01-02"))
	}
	return strings.TrimSpace(b.String())
}

// FormatMemoriesDocument builds a synthetic source from ChatGPT memory.json entries.
func FormatMemoriesDocument(entries []MemoryEntry) string {
	var b strings.Builder
	b.WriteString("# ChatGPT saved memories\n\n")
	for _, e := range entries {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(e.Content))
		b.WriteString("\n")
	}
	b.WriteString("\n— Imported from ChatGPT memory.json")
	return strings.TrimSpace(b.String())
}
