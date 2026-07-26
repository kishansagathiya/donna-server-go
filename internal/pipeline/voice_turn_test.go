package pipeline

import (
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

func TestVoiceTurnResult_isTranscriptOnly(t *testing.T) {
	// Talk-mode voice must not produce a parallel chat reply. Clients send the
	// transcript through RunTextTurn / POST /chat instead.
	result := TurnResult{
		Transcript: "hello donna",
		Timings:    protocol.EmptyTurnTimings(),
	}
	if result.ReplyText != "" {
		t.Fatalf("unexpected reply text %q", result.ReplyText)
	}
	if result.AssistantAudio != nil {
		t.Fatal("unexpected assistant audio on voice STT result")
	}
	if result.UsedRetry {
		t.Fatal("retry TTS path should not exist on STT-only voice turns")
	}
}
