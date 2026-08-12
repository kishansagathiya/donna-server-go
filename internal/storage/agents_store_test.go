package storage

import (
	"encoding/json"
	"testing"
)

func TestMergeUserFinishedResult(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"kind":     "ask_user",
		"question": "Which airport?",
		"options":  []any{map[string]any{"id": "sfo", "label": "SFO"}},
	})
	got := MergeUserFinishedResult(raw)
	if got["closed_by_user"] != true {
		t.Fatalf("expected closed_by_user, got %#v", got)
	}
	if got["summary"] != "Which airport?" {
		t.Fatalf("expected question as summary, got %#v", got["summary"])
	}
	if got["kind"] != "ask_user" {
		t.Fatalf("expected prior fields kept, got %#v", got)
	}

	empty := MergeUserFinishedResult(nil)
	if empty["summary"] != "Marked finished by user." {
		t.Fatalf("empty summary: %#v", empty)
	}
}
