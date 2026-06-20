package notes

import "testing"

func TestExtractTitle(t *testing.T) {
	if got := ExtractTitle("Hello world\nSecond line"); got != "Hello world" {
		t.Fatalf("ExtractTitle = %q", got)
	}
	long := stringsRepeat("a", 60)
	if got := ExtractTitle(long); len(got) != 53 { // 50 + "..."
		t.Fatalf("ExtractTitle long = %q len=%d", got, len(got))
	}
}

func TestExtractVoiceUserContent(t *testing.T) {
	got := ExtractVoiceUserContent("User: remember milk\nAssistant: Got it.")
	if got != "remember milk" {
		t.Fatalf("ExtractVoiceUserContent = %q", got)
	}
}

func TestExtractPreview(t *testing.T) {
	got := ExtractPreview("Title line\nSecond line here")
	if got != "Second line here" {
		t.Fatalf("ExtractPreview = %q", got)
	}
}

func TestExtractPreview_truncatesLongPreview(t *testing.T) {
	long := stringsRepeat("word ", 30)
	got := ExtractPreview("Title\n" + long)
	if len(got) != 83 { // 80 + "..."
		t.Fatalf("ExtractPreview len = %d, want 83", len(got))
	}
	if got[len(got)-3:] != "..." {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
}

func TestExtractTitle_emptyContent(t *testing.T) {
	if got := ExtractTitle("   \n  "); got != "Untitled Note" {
		t.Fatalf("ExtractTitle empty = %q", got)
	}
}

func stringsRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
