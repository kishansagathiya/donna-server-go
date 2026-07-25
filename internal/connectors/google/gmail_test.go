package google

import (
	"strings"
	"testing"
)

func TestBuildRFC822Message(t *testing.T) {
	raw, err := buildRFC822Message("a@example.com", "b@example.com", "Hello", "Body text")
	if err != nil {
		t.Fatal(err)
	}
	msg := string(raw)
	for _, want := range []string{
		"To: a@example.com\r\n",
		"Cc: b@example.com\r\n",
		"Subject: Hello\r\n",
		"\r\nBody text",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in:\n%s", want, msg)
		}
	}
}

func TestBuildRFC822MessageStripsHeaderInjection(t *testing.T) {
	raw, err := buildRFC822Message("a@example.com", "", "Hi\nBcc: evil@x.com", "ok")
	if err != nil {
		t.Fatal(err)
	}
	msg := string(raw)
	if strings.Contains(msg, "\nBcc:") || strings.Contains(msg, "\r\nBcc:") {
		t.Fatalf("header injection not stripped: %s", msg)
	}
	// Newlines are removed from the subject value; Bcc cannot become its own header.
	if !strings.Contains(msg, "Subject: HiBcc: evil@x.com\r\n") {
		t.Fatalf("expected collapsed subject, got:\n%s", msg)
	}
}

func TestBuildRFC822MessageRequiresRecipient(t *testing.T) {
	if _, err := buildRFC822Message("", "", "Hi", "body"); err == nil {
		t.Fatal("expected recipient_required")
	}
}
