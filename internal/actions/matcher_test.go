package actions

import "testing"

func TestKindToActionSlug(t *testing.T) {
	cases := map[string]string{
		"remind":        "propose_reminder",
		"follow_up":     "draft_message",
		"draft_message": "draft_message",
		"email":         "send_email",
		"send_email":    "send_email",
		"open_url":      "open_url",
		"schedule":      "create_calendar_event",
		"calendar":      "create_calendar_event",
	}
	for kind, want := range cases {
		if got := kindToActionSlug[kind]; got != want {
			t.Fatalf("kind %s: got %s want %s", kind, got, want)
		}
	}
	if _, ok := kindToActionSlug["create_note"]; ok {
		t.Fatal("create_note must not map to an action")
	}
}

func TestShouldSpawnCloudAgent(t *testing.T) {
	for _, kind := range []string{"find_media", "research", "research_and_act", "book_travel"} {
		if !ShouldSpawnCloudAgent(kind) {
			t.Fatalf("%s should spawn", kind)
		}
	}
	if ShouldSpawnCloudAgent("schedule") || ShouldSpawnCloudAgent("agent_result") {
		t.Fatal("builtins / results must not spawn")
	}
}
