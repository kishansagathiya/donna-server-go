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
	h.persistTurn("user", "session", "hi", "hello", nil, protocol.TurnTimings{}, false)
}

func TestHandler_persistTurn_skipsWhenSkipped(t *testing.T) {
	h := &Handler{
		Conversations: &storage.Conversations{Enabled: true},
	}
	h.persistTurn("user", "session", "hi", "hello", nil, protocol.TurnTimings{}, true)
}
