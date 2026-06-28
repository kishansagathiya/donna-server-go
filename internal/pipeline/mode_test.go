package pipeline

import "testing"

func TestParseMode(t *testing.T) {
	if got := ParseMode("notes"); got != ModeNotes {
		t.Fatalf("ParseMode(notes) = %q", got)
	}
	if got := ParseMode("listen"); got != ModeNotes {
		t.Fatalf("ParseMode(listen) = %q", got)
	}
	if got := ParseMode("talk"); got != ModeTalk {
		t.Fatalf("ParseMode(talk) = %q", got)
	}
	if got := ParseMode(""); got != ModeTalk {
		t.Fatalf("ParseMode(empty) = %q", got)
	}
}

func TestInteractionMode_IsNotes(t *testing.T) {
	if !ModeNotes.IsNotes() {
		t.Fatal("ModeNotes should be notes")
	}
	if ModeTalk.IsNotes() {
		t.Fatal("ModeTalk should not be notes")
	}
}
