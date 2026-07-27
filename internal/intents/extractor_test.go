package intents

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHeuristicExtractRemind(t *testing.T) {
	out := heuristicExtract("Remind me to call Mom Saturday about flights")
	if len(out) != 1 || out[0].Kind != "remind" {
		t.Fatalf("expected remind intent, got %#v", out)
	}
}

func TestHeuristicExtractFollowUp(t *testing.T) {
	out := heuristicExtract("Follow up with Alex about the contract")
	if len(out) != 1 || out[0].Kind != "follow_up" {
		t.Fatalf("expected follow_up intent, got %#v", out)
	}
}

func TestHeuristicExtractURL(t *testing.T) {
	out := heuristicExtract("Check https://example.com/docs later")
	if len(out) != 1 || out[0].Kind != "open_url" {
		t.Fatalf("expected open_url intent, got %#v", out)
	}
	if out[0].Slots["url"] != "https://example.com/docs" {
		t.Fatalf("unexpected slots: %#v", out[0].Slots)
	}
}

func TestHeuristicExtractScheduleWithWhenAndAttendees(t *testing.T) {
	out := heuristicExtract("Schedule a meeting with alex@example.com tomorrow at 3pm")
	if len(out) != 1 || out[0].Kind != "schedule" {
		t.Fatalf("expected schedule intent, got %#v", out)
	}
	if out[0].Slots["attendees"] != "alex@example.com" {
		t.Fatalf("attendees: %#v", out[0].Slots)
	}
	if out[0].Slots["when"] == "" {
		t.Fatalf("expected when slot, got %#v", out[0].Slots)
	}
}

func TestBuildExtractorUserMessageIncludesCurrentTime(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	msg := BuildExtractorUserMessageAt("note", "Inbox", "Schedule lunch tomorrow", now)
	if !strings.Contains(msg, "current_time_utc: 2026-07-27T12:00:00Z") {
		t.Fatalf("missing current time context: %s", msg)
	}
}

func TestParseExtractorOutput(t *testing.T) {
	raw := "Here you go:\n```json\n{\"intents\":[{\"kind\":\"remind\",\"summary\":\"Call Mom\",\"slots\":{\"when\":\"Saturday\"},\"confidence\":0.9}]}\n```"
	out := parseExtractorOutput(raw)
	if len(out.Intents) != 1 {
		t.Fatalf("expected 1 intent, got %#v", out)
	}
	if out.Intents[0].Kind != "remind" {
		t.Fatalf("unexpected kind: %#v", out.Intents[0])
	}
}

func TestNormalizeKindRejectsCreateNotePath(t *testing.T) {
	if normalizeKind("create_note") != "create_note" {
		t.Fatal("normalize should keep create_note so extractor can skip it")
	}
	if normalizeKind("Draft Message") != "draft_message" {
		t.Fatal("expected draft_message")
	}
}

func TestSlotsRoundTrip(t *testing.T) {
	slots := normalizeSlots(map[string]string{" title ": " Call Mom ", "": "x", "a": ""})
	b, err := json.Marshal(slots)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"title":"Call Mom"}` {
		t.Fatalf("unexpected slots json: %s", b)
	}
}
