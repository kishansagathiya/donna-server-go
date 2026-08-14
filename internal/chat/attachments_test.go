package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
)

func TestMain(m *testing.M) {
	ingest.InitExtractors(ingest.Services{})
	m.Run()
}

func TestGroundChatTurnEmpty(t *testing.T) {
	_, err := groundChatTurn(context.Background(), "  ", nil)
	if err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestGroundChatTurnTextOnly(t *testing.T) {
	got, err := groundChatTurn(context.Background(), "Hello Donna", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.GroundedMessage != "Hello Donna" || got.DisplayMessage != "Hello Donna" {
		t.Fatalf("unexpected grounding: %+v", got)
	}
}

func TestGroundChatTurnFileAttachment(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("alpha beta gamma"))
	got, err := groundChatTurn(context.Background(), "Summarize this", []ChatAttachment{{
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
	got, err := groundChatTurn(context.Background(), "", []ChatAttachment{{
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
	_, err := groundChatTurn(context.Background(), "hi", atts)
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("expected too many attachments error, got %v", err)
	}
}

func TestSplitGroundedTranscript(t *testing.T) {
	got, err := groundChatTurn(
		context.Background(),
		"What is in this photo?\n\nThe user shared the following attachment(s) for this turn only (not saved to long-term memory unless they ask):\n\nAttached: photo.png\n\nImage: a cat",
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.DisplayMessage != "What is in this photo?" {
		t.Fatalf("DisplayMessage = %q", got.DisplayMessage)
	}
	if !strings.Contains(got.GroundedMessage, "Image: a cat") {
		t.Fatalf("GroundedMessage missing vision text: %q", got.GroundedMessage)
	}
}

func TestGroundChatTurnTweetCapture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"tweet": map[string]any{
				"id":   "123",
				"url":  "https://x.com/karpathy/status/123",
				"text": "compiled wiki > RAG",
				"author": map[string]any{
					"name":        "Andrej Karpathy",
					"screen_name": "karpathy",
				},
			},
		})
	}))
	defer srv.Close()
	restore := ingest.SetTweetEndpointsForTest(srv.URL, "", srv.Client())
	defer restore()

	got, err := groundChatTurn(context.Background(), "worth saving https://x.com/karpathy/status/123", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Captures) != 1 {
		t.Fatalf("Captures = %#v", got.Captures)
	}
	if !strings.Contains(got.GroundedMessage, "compiled wiki > RAG") {
		t.Fatalf("GroundedMessage missing tweet: %q", got.GroundedMessage)
	}
	if !strings.Contains(got.GroundedMessage, "save into memory") {
		t.Fatalf("GroundedMessage missing capture preamble: %q", got.GroundedMessage)
	}
}

func TestGroundChatTurnExtractsAttachmentsInParallel(t *testing.T) {
	var inFlight atomic.Int32
	var maxFlight atomic.Int32
	orig := extractOneAttachment
	extractOneAttachment = func(_ context.Context, att ChatAttachment) (string, string, ingest.ExtractedAsset, error) {
		n := inFlight.Add(1)
		for {
			cur := maxFlight.Load()
			if n <= cur || maxFlight.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		inFlight.Add(-1)
		return att.Filename, "text-" + att.Filename, ingest.ExtractedAsset{}, nil
	}
	t.Cleanup(func() { extractOneAttachment = orig })

	start := time.Now()
	got, err := groundChatTurn(context.Background(), "look", []ChatAttachment{
		{Kind: "file", Filename: "a.png"},
		{Kind: "file", Filename: "b.png"},
		{Kind: "file", Filename: "c.png"},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if maxFlight.Load() < 2 {
		t.Fatalf("expected overlapping extraction, max in-flight = %d", maxFlight.Load())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("extraction looked sequential: %s", elapsed)
	}
	if strings.Join(got.Labels, ",") != "a.png,b.png,c.png" {
		t.Fatalf("order not preserved: %#v", got.Labels)
	}
}

func TestImageDataURLReusesRawBase64(t *testing.T) {
	if got := imageDataURL("image/jpeg", "abc123"); got != "data:image/jpeg;base64,abc123" {
		t.Fatalf("imageDataURL = %q", got)
	}
	raw := "data:image/png;base64,xyz"
	if got := imageDataURL("image/png", raw); got != raw {
		t.Fatalf("expected passthrough, got %q", got)
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
