package storage

import "testing"

func TestNoteSummaryRow_FoldsAudioPathIntoHasAudio(t *testing.T) {
	path := "abc/def.wav"
	rows := []noteSummaryRow{
		{NoteSummary: NoteSummary{ID: "n1"}, AudioPath: &path},
		{NoteSummary: NoteSummary{ID: "n2"}, AudioPath: nil},
		{NoteSummary: NoteSummary{ID: "n3"}, AudioPath: strPtr("   ")},
	}

	got := []NoteSummary{rows[0].toSummary(), rows[1].toSummary(), rows[2].toSummary()}

	if !got[0].HasAudio {
		t.Fatalf("note with non-empty audio_path should report has_audio=true, got %+v", got[0])
	}
	if got[1].HasAudio {
		t.Fatalf("note with nil audio_path should report has_audio=false, got %+v", got[1])
	}
	if got[2].HasAudio {
		t.Fatalf("note with whitespace-only audio_path should report has_audio=false, got %+v", got[2])
	}
}

func TestNote_HasAudio(t *testing.T) {
	if (Note{AudioPath: nil}).HasAudio() {
		t.Fatal("nil AudioPath must yield HasAudio=false")
	}
	if (Note{AudioPath: strPtr("")}).HasAudio() {
		t.Fatal("empty AudioPath must yield HasAudio=false")
	}
	if !(Note{AudioPath: strPtr("notes/u/x.wav")}).HasAudio() {
		t.Fatal("non-empty AudioPath must yield HasAudio=true")
	}
}

func strPtr(s string) *string { return &s }