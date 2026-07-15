package chat

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
)

func TestMain(m *testing.M) {
	ingest.InitExtractors(ingest.Services{})
	m.Run()
}

func TestGroundChatTurnEmpty(t *testing.T) {
	_, err := groundChatTurn("  ", nil)
	if err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestGroundChatTurnTextOnly(t *testing.T) {
	got, err := groundChatTurn("Hello Donna", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.GroundedMessage != "Hello Donna" || got.DisplayMessage != "Hello Donna" {
		t.Fatalf("unexpected grounding: %+v", got)
	}
}

func TestGroundChatTurnFileAttachment(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("alpha beta gamma"))
	got, err := groundChatTurn("Summarize this", []ChatAttachment{{
		Kind:       "file",
		Filename:   "notes.txt",
		Mime:       "text/plain",
		DataBase64: payload,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.DisplayMessage != "Summarize this\n\n📎 notes.txt" {
		t.Fatalf("DisplayMessage = %q", got.DisplayMessage)
	}
	if !strings.Contains(got.GroundedMessage, "alpha beta gamma") {
		t.Fatalf("GroundedMessage missing file content: %q", got.GroundedMessage)
	}
	if !strings.Contains(got.GroundedMessage, "not saved to long-term memory") {
		t.Fatalf("GroundedMessage missing ephemeral guidance: %q", got.GroundedMessage)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "notes.txt" {
		t.Fatalf("Labels = %#v", got.Labels)
	}
}

func TestGroundChatTurnAttachmentOnly(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("just the file"))
	got, err := groundChatTurn("", []ChatAttachment{{
		Kind:       "file",
		Filename:   "solo.txt",
		Mime:       "text/plain",
		DataBase64: payload,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.DisplayMessage != "📎 solo.txt" {
		t.Fatalf("DisplayMessage = %q", got.DisplayMessage)
	}
	if !strings.Contains(got.GroundedMessage, "just the file") {
		t.Fatalf("GroundedMessage missing content: %q", got.GroundedMessage)
	}
}

func TestGroundChatTurnTooManyAttachments(t *testing.T) {
	atts := make([]ChatAttachment, maxChatAttachments+1)
	for i := range atts {
		atts[i] = ChatAttachment{Kind: "url", URL: "https://example.com"}
	}
	_, err := groundChatTurn("hi", atts)
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("expected too many attachments error, got %v", err)
	}
}

func TestLoneURL(t *testing.T) {
	if got := loneURL("https://example.com/doc"); got != "https://example.com/doc" {
		t.Fatalf("loneURL = %q", got)
	}
	if got := loneURL("see https://example.com"); got != "" {
		t.Fatalf("expected empty for multi-word, got %q", got)
	}
}

func TestAssertPublicHTTPURLBlocksLocalhost(t *testing.T) {
	if err := assertPublicHTTPURL("http://localhost/secret"); err == nil {
		t.Fatal("expected localhost block")
	}
	if err := assertPublicHTTPURL("https://127.0.0.1/"); err == nil {
		t.Fatal("expected loopback block")
	}
}
