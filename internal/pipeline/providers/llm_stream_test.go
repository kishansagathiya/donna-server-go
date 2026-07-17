package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseStreamChunk_midStreamError(t *testing.T) {
	payload := `{"id":"cmpl-1","error":{"code":"server_error","message":"Provider disconnected unexpectedly"},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}`
	_, _, _, err := parseStreamChunk(payload)
	if err == nil {
		t.Fatal("expected mid-stream error")
	}
	if !strings.Contains(err.Error(), "Provider disconnected unexpectedly") {
		t.Fatalf("error = %q, want provider message", err.Error())
	}
}

func TestParseStreamChunk_contentFilter(t *testing.T) {
	payload := `{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"content_filter"}]}`
	_, _, _, err := parseStreamChunk(payload)
	if err == nil {
		t.Fatal("expected content filter error")
	}
}

func TestParseStreamChunk_contentDelta(t *testing.T) {
	payload := `{"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`
	text, _, activity, err := parseStreamChunk(payload)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello" {
		t.Fatalf("text = %q, want Hello", text)
	}
	if activity {
		t.Fatal("content delta should not be marked as reasoning activity")
	}
}

func TestParseStreamChunk_reasoningActivity(t *testing.T) {
	payload := `{"choices":[{"index":0,"delta":{"reasoning":"thinking..."},"finish_reason":null}]}`
	text, _, activity, err := parseStreamChunk(payload)
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Fatalf("text = %q, want empty", text)
	}
	if !activity {
		t.Fatal("expected reasoning activity")
	}
}

func TestStreamCompletionWithOptions_rejectsEmptyReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	llm := NewLLM("test-key", "provider/model", "")
	llm.BaseURL = srv.URL
	llm.Client = srv.Client()

	_, err := llm.StreamCompletionWithOptions(context.Background(), []ChatMessage{
		{Role: "user", Content: "hi"},
	}, ChatCompletionOptions{}, nil)
	if err == nil {
		t.Fatal("expected empty reply error")
	}
	if !strings.Contains(err.Error(), "empty reply") {
		t.Fatalf("error = %q, want empty reply", err.Error())
	}
}

func TestStreamCompletionWithOptions_surfacesMidStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"Provider disconnected unexpectedly\"},\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"error\"}]}\n\n"))
	}))
	defer srv.Close()

	llm := NewLLM("test-key", "provider/model", "")
	llm.BaseURL = srv.URL
	llm.Client = srv.Client()

	var got strings.Builder
	_, err := llm.StreamCompletionWithOptions(context.Background(), []ChatMessage{
		{Role: "user", Content: "hi"},
	}, ChatCompletionOptions{}, func(chunk string) error {
		got.WriteString(chunk)
		return nil
	})
	if err == nil {
		t.Fatal("expected mid-stream error")
	}
	if got.String() != "Hi" {
		t.Fatalf("partial content = %q, want Hi", got.String())
	}
	if !strings.Contains(err.Error(), "Provider disconnected unexpectedly") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestStreamCompletionWithOptions_streamsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": OPENROUTER PROCESSING\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	llm := NewLLM("test-key", "provider/model", "")
	llm.BaseURL = srv.URL
	llm.Client = srv.Client()

	var got strings.Builder
	_, err := llm.StreamCompletionWithOptions(context.Background(), []ChatMessage{
		{Role: "user", Content: "hi"},
	}, ChatCompletionOptions{}, func(chunk string) error {
		got.WriteString(chunk)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "Hello" {
		t.Fatalf("got %q, want Hello", got.String())
	}
}
