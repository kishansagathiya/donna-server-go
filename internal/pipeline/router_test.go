package pipeline

import "testing"

func TestShouldUseFastModel_chitchatAndShort(t *testing.T) {
	if !shouldUseFastModel("hey") {
		t.Fatal("expected chitchat on fast path")
	}
	if !shouldUseFastModel("What time is it?") {
		t.Fatal("expected short prompt on fast path")
	}
}

func TestShouldUseFastModel_complexAndMemory(t *testing.T) {
	if shouldUseFastModel("Compare the pros and cons of these three roadmaps in detail") {
		t.Fatal("expected complex prompt on strong path")
	}
	if shouldUseFastModel("What do you remember about my coffee preferences?") {
		t.Fatal("expected memory prompt on strong path")
	}
}

func TestCountWords(t *testing.T) {
	if got := countWords("one two  three"); got != 3 {
		t.Fatalf("got %d want 3", got)
	}
}
