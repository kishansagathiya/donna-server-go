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

func stringsRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
