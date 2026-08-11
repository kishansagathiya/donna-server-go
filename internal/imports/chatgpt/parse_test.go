package chatgpt

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"
)

func TestFlattenConversationPath(t *testing.T) {
	root := "root"
	parentA := "a"
	mapping := map[string]rawMappingNode{
		"root": {ID: "root", Parent: nil, Children: []string{"a"}},
		"a": {
			ID:     "a",
			Parent: &root,
			Children: []string{"b"},
			Message: &rawMessage{
				Author:  rawAuthor{Role: "user"},
				Content: json.RawMessage(`{"content_type":"text","parts":["Hello Donna"]}`),
			},
		},
		"b": {
			ID:     "b",
			Parent: &parentA,
			Children: []string{},
			Message: &rawMessage{
				Author:  rawAuthor{Role: "assistant"},
				Content: json.RawMessage(`{"content_type":"text","parts":["Hi there"]}`),
			},
		},
		// Branch that should be ignored (not on current_node path).
		"alt": {
			ID:     "alt",
			Parent: &parentA,
			Message: &rawMessage{
				Author:  rawAuthor{Role: "assistant"},
				Content: json.RawMessage(`{"content_type":"text","parts":["Ignored branch"]}`),
			},
		},
	}

	msgs := FlattenConversationPath("b", mapping)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "Hello Donna" {
		t.Fatalf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Hi there" {
		t.Fatalf("unexpected second message: %+v", msgs[1])
	}
}

func TestParseExportZIP_ConversationsAndMemory(t *testing.T) {
	createTime := 1700000000.0
	updateTime := 1700000100.0
	root := "root"
	parentU := "u1"
	convs := []rawConversation{
		{
			ConversationID: "conv-1",
			Title:          "Favorite food",
			CreateTime:     &createTime,
			UpdateTime:     &updateTime,
			CurrentNode:    "a1",
			Mapping: map[string]rawMappingNode{
				"root": {ID: "root", Parent: nil, Children: []string{"u1"}},
				"u1": {
					ID:     "u1",
					Parent: &root,
					Children: []string{"a1"},
					Message: &rawMessage{
						Author:  rawAuthor{Role: "user"},
						Content: json.RawMessage(`{"content_type":"text","parts":["I love sushi"]}`),
					},
				},
				"a1": {
					ID:     "a1",
					Parent: &parentU,
					Message: &rawMessage{
						Author:  rawAuthor{Role: "assistant"},
						Content: json.RawMessage(`{"content_type":"text","parts":["Got it"]}`),
					},
				},
			},
		},
		{
			ID:          "conv-2",
			Title:       "Timezone",
			CreateTime:  &createTime,
			CurrentNode: "u2",
			Mapping: map[string]rawMappingNode{
				"root2": {ID: "root2", Parent: nil, Children: []string{"u2"}},
				"u2": {
					ID: "u2",
					Parent: func() *string {
						s := "root2"
						return &s
					}(),
					Message: &rawMessage{
						Author:  rawAuthor{Role: "user"},
						Content: json.RawMessage(`{"content_type":"text","parts":["I live in IST"]}`),
					},
				},
			},
		},
	}
	convJSON, err := json.Marshal(convs)
	if err != nil {
		t.Fatal(err)
	}
	memJSON := []byte(`[
		{"id":"m1","content":"User prefers dark mode"},
		{"id":"m2","memory":"User's name is Sam"}
	]`)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("conversations.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(convJSON); err != nil {
		t.Fatal(err)
	}
	w2, err := zw.Create("memory.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Write(memJSON); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	payload, err := ParseExportZIP(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Conversations) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(payload.Conversations))
	}
	if payload.Conversations[0].ID != "conv-1" {
		t.Fatalf("unexpected id: %s", payload.Conversations[0].ID)
	}
	if got := FormatConversationNote(payload.Conversations[0]); !containsAll(got, "Favorite food", "User: I love sushi", "Assistant: Got it", "From ChatGPT") {
		t.Fatalf("unexpected note format:\n%s", got)
	}
	if len(payload.Memories) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(payload.Memories))
	}
	doc := FormatMemoriesDocument(payload.Memories)
	if !containsAll(doc, "prefers dark mode", "name is Sam") {
		t.Fatalf("unexpected memories doc:\n%s", doc)
	}
}

func TestParseExportZIP_SplitConversationFiles(t *testing.T) {
	mk := func(id, text string) rawConversation {
		root := "root-" + id
		return rawConversation{
			ConversationID: id,
			Title:          id,
			CurrentNode:    "u-" + id,
			Mapping: map[string]rawMappingNode{
				root: {ID: root, Parent: nil, Children: []string{"u-" + id}},
				"u-" + id: {
					ID: "u-" + id,
					Parent: func() *string {
						s := root
						return &s
					}(),
					Message: &rawMessage{
						Author:  rawAuthor{Role: "user"},
						Content: mustRaw(`{"content_type":"text","parts":[` + jsonString(text) + `]}`),
					},
				},
			},
		}
	}
	a, _ := json.Marshal([]rawConversation{mk("c0", "alpha")})
	b, _ := json.Marshal([]rawConversation{mk("c1", "beta")})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w0, _ := zw.Create("conversations-000.json")
	_, _ = w0.Write(a)
	w1, _ := zw.Create("conversations-001.json")
	_, _ = w1.Write(b)
	_ = zw.Close()

	payload, err := ParseExportZIP(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Conversations) != 2 {
		t.Fatalf("expected 2 conversations from split files, got %d", len(payload.Conversations))
	}
}

func TestParseExportZIP_MissingConversations(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("chat.html")
	_, _ = w.Write([]byte("<html></html>"))
	_ = zw.Close()
	if _, err := ParseExportZIP(buf.Bytes()); err == nil {
		t.Fatal("expected error when conversations.json missing")
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !bytes.Contains([]byte(s), []byte(p)) {
			return false
		}
	}
	return true
}

func mustRaw(s string) json.RawMessage { return json.RawMessage(s) }

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
