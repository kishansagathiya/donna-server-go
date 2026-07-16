package actions

import "testing"

func TestKindToActionSlug(t *testing.T) {
	cases := map[string]string{
		"remind":        "propose_reminder",
		"follow_up":     "draft_message",
		"draft_message": "draft_message",
		"open_url":      "open_url",
		"schedule":      "propose_reminder",
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
