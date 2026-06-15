package pipeline

import "testing"

func TestSentenceBuffer(t *testing.T) {
	buf := &sentenceBuffer{}
	got := buf.add("Hello there. How are you? ")
	if len(got) != 2 || got[0] != "Hello there." || got[1] != "How are you?" {
		t.Fatalf("unexpected sentences: %#v", got)
	}
	if rest := buf.flush(); rest != "" {
		t.Fatalf("expected empty flush, got %q", rest)
	}
}

func TestSentenceBufferFlushRemainder(t *testing.T) {
	buf := &sentenceBuffer{}
	if got := buf.add("No punctuation here"); len(got) != 0 {
		t.Fatalf("expected no sentences yet, got %#v", got)
	}
	if rest := buf.flush(); rest != "No punctuation here" {
		t.Fatalf("unexpected remainder: %q", rest)
	}
}
