package pipeline

import "testing"

func TestParseMode(t *testing.T) {
	if got := ParseMode("listen"); got != ModeListen {
		t.Fatalf("ParseMode(listen) = %q", got)
	}
	if got := ParseMode("talk"); got != ModeTalk {
		t.Fatalf("ParseMode(talk) = %q", got)
	}
	if got := ParseMode(""); got != ModeTalk {
		t.Fatalf("ParseMode(empty) = %q", got)
	}
}

func TestInteractionMode_IsListen(t *testing.T) {
	if !ModeListen.IsListen() {
		t.Fatal("ModeListen should be listen")
	}
	if ModeTalk.IsListen() {
		t.Fatal("ModeTalk should not be listen")
	}
}
