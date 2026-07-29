package tts

import (
	"strings"
	"testing"
)

func TestObjectPath(t *testing.T) {
	a := objectPath("user-1", "cartesia|sonic-3.5|kiara", "Hello Donna", "wav")
	b := objectPath("user-1", "cartesia|sonic-3.5|kiara", "Hello Donna", "wav")
	c := objectPath("user-1", "cartesia|sonic-3.5|kiara", "Hello other", "wav")
	d := objectPath("user-2", "cartesia|sonic-3.5|kiara", "Hello Donna", "wav")

	if a != b {
		t.Fatalf("same inputs should hash equally: %q vs %q", a, b)
	}
	if a == c {
		t.Fatal("different text should produce different paths")
	}
	if a == d {
		t.Fatal("different users should produce different paths")
	}
	if !strings.HasPrefix(a, "user-1/tts/") || !strings.HasSuffix(a, ".wav") {
		t.Fatalf("unexpected path shape %q", a)
	}
}
