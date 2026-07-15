package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

func TestPackHistory_underCapUnchanged(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	got := PackHistory(context.Background(), nil, history, 20)
	if len(got) != 2 {
		t.Fatalf("expected unchanged history, got %#v", got)
	}
}

func TestPackHistory_nilLLMFallsBackToTruncate(t *testing.T) {
	history := make([]providers.ChatMessage, 0, 6)
	for i := 0; i < 6; i++ {
		history = append(history, providers.ChatMessage{
			Role:    "user",
			Content: strings.Repeat("x", i+1),
		})
	}
	got := PackHistory(context.Background(), nil, history, 2)
	if len(got) != 2 {
		t.Fatalf("expected last 2 messages, got %#v", got)
	}
	if got[0].Content != "xxxxx" || got[1].Content != "xxxxxx" {
		t.Fatalf("unexpected truncated content: %#v", got)
	}
}

func TestPackHistory_zeroMaxReturnsInput(t *testing.T) {
	history := []providers.ChatMessage{{Role: "user", Content: "hi"}}
	got := PackHistory(context.Background(), nil, history, 0)
	if len(got) != 1 {
		t.Fatalf("expected input preserved: %#v", got)
	}
}
