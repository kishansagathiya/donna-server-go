package chat

import (
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestTextTurnIndex(t *testing.T) {
	tests := []struct {
		name    string
		history []providers.ChatMessage
		want    int
	}{
		{
			name:    "empty history",
			history: nil,
			want:    0,
		},
		{
			name: "one prior turn",
			history: []providers.ChatMessage{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello"},
			},
			want: 1,
		},
		{
			name: "two prior turns",
			history: []providers.ChatMessage{
				{Role: "user", Content: "one"},
				{Role: "assistant", Content: "two"},
				{Role: "user", Content: "three"},
				{Role: "assistant", Content: "four"},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := textTurnIndex(tt.history); got != tt.want {
				t.Fatalf("textTurnIndex() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHandler_persistTurn_skipsWhenDisabled(t *testing.T) {
	h := &Handler{Conversations: nil}
	h.persistTurn("user", "session", groundedTurn{
		DisplayMessage:  "hi",
		GroundedMessage: "hi",
	}, nil, "hello", nil, protocol.TurnTimings{})
}

func TestHandler_persistTurn_skipsWhenStoreDisabled(t *testing.T) {
	h := &Handler{
		Conversations: &storage.Conversations{Enabled: false},
	}
	h.persistTurn("user", "session", groundedTurn{
		DisplayMessage:  "hi",
		GroundedMessage: "hi",
	}, nil, "hello", nil, protocol.TurnTimings{})
}

func TestPreviewGroundingStatus(t *testing.T) {
	phase, host := previewGroundingStatus("hi", []ChatAttachment{{
		Kind: "image",
		Mime: "image/png",
	}})
	if phase != protocol.TurnPhaseAnalyzing || host != "" {
		t.Fatalf("image preview = %q %q", phase, host)
	}

	phase, host = previewGroundingStatus("", []ChatAttachment{{
		Kind: "url",
		URL:  "https://example.com/page",
	}})
	if phase != protocol.TurnPhaseFetching || host != "example.com" {
		t.Fatalf("url preview = %q %q", phase, host)
	}

	phase, host = previewGroundingStatus("https://news.ycombinator.com", nil)
	if phase != protocol.TurnPhaseFetching || host != "news.ycombinator.com" {
		t.Fatalf("lone url preview = %q %q", phase, host)
	}

	phase, host = previewGroundingStatus("just chatting", nil)
	if phase != "" || host != "" {
		t.Fatalf("plain text preview = %q %q", phase, host)
	}

	phase, host = previewGroundingStatus("see https://x.com/karpathy/status/1 later", nil)
	if phase != protocol.TurnPhaseFetching || host != "x.com" {
		t.Fatalf("tweet preview = %q %q", phase, host)
	}
}
