package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTurnDoneJSON(t *testing.T) {
	raw, err := SerializeServerMessage(TurnDone(TurnTimings{
		STTMs: 4030, AugmentMs: 0, LLMFirstTokenMs: 856, TTSFirstByteMs: 1324, TotalMs: 7489,
	}, false))
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("empty json")
	}
}

func TestTurnDoneWithNoteIDJSON(t *testing.T) {
	raw, err := SerializeServerMessage(TurnDoneWithNoteID(TurnTimings{
		STTMs: 10, TotalMs: 20,
	}, false, "11111111-1111-4111-8111-111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"noteId":"11111111-1111-4111-8111-111111111111"`) {
		t.Fatalf("expected noteId in payload, got %s", raw)
	}
	if strings.Contains(raw, `"skipped"`) {
		t.Fatalf("did not expect skipped for successful turn: %s", raw)
	}
}

func TestParseClientMessage_clientNoteId(t *testing.T) {
	msg, err := ParseClientMessage(`{"type":"session.start","mode":"notes","clientNoteId":"11111111-1111-4111-8111-111111111111"}`)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ClientNoteID == nil || *msg.ClientNoteID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected clientNoteId: %#v", msg.ClientNoteID)
	}
}

func TestSerializeParse_roundTrip(t *testing.T) {
	tests := []ServerMessage{
		SessionReady("sess-1", "user-1"),
		TurnPhaseMessage(TurnPhaseGenerating),
		ErrorMessage("turn_failed", "something broke"),
	}

	for _, msg := range tests {
		raw, err := SerializeServerMessage(msg)
		if err != nil {
			t.Fatalf("serialize %#v: %v", msg, err)
		}

		var parsed ServerMessage
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if parsed.Type != msg.Type {
			t.Fatalf("type mismatch: got %q want %q", parsed.Type, msg.Type)
		}
	}
}

func TestParseClientMessage_missingType(t *testing.T) {
	_, err := ParseClientMessage(`{"userId":"u1"}`)
	if err == nil || !strings.Contains(err.Error(), "missing type") {
		t.Fatalf("expected missing type error, got %v", err)
	}
}

func TestParseClientMessage_success(t *testing.T) {
	msg, err := ParseClientMessage(`{"type":"session.start","userId":"u1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != "session.start" {
		t.Fatalf("unexpected type %q", msg.Type)
	}
}
