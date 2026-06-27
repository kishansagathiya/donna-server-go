package storage

import "testing"

func TestDeriveConversationTitle_prefersUser(t *testing.T) {
	got := deriveConversationTitle("Plan my trip to Tokyo", "Sure, let's start with dates.")
	if got != "Plan my trip to Tokyo" {
		t.Fatalf("got %q", got)
	}
}

func TestDeriveConversationTitle_fallsBackToAssistant(t *testing.T) {
	got := deriveConversationTitle("", "Here is a summary of your notes.")
	if got != "Here is a summary of your notes." {
		t.Fatalf("got %q", got)
	}
}

func TestFallbackConversationTitle_voice(t *testing.T) {
	got := fallbackConversationTitle("voice", "2026-06-01T10:00:00Z")
	if got != "Voice chat · Jun 1" {
		t.Fatalf("got %q", got)
	}
}
