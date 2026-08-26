package agents

import "testing"

func TestLooksLikeSecret(t *testing.T) {
	if !looksLikeSecret("4111111111111111") {
		t.Fatal("card-like")
	}
	if looksLikeSecret("SFO") {
		t.Fatal("ordinary text")
	}
}

func TestRedirectLedgerStatus(t *testing.T) {
	if got := RedirectLedgerStatus("Approved."); got != "succeeded" {
		t.Fatalf("got %s", got)
	}
	if got := RedirectLedgerStatus("Denied"); got != "cancelled" {
		t.Fatalf("got %s", got)
	}
	if got := RedirectLedgerStatus("Prefer United"); got != "cancelled" {
		t.Fatalf("steer should drop proposal, got %s", got)
	}
}

func TestInteractiveBrowserToolsRegister(t *testing.T) {
	reg := DefaultToolsets(nil, nil, "http://127.0.0.1:9", nil, nil)
	for _, name := range []string{"browse_page", "browser_navigate", "browser_snapshot", "browser_click", "browser_type"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("missing %s", name)
		}
	}
	if _, ok := DefaultToolsets(nil, nil, "", nil, nil).Get("browser_click"); ok {
		t.Fatal("interactive tools should not register without sidecar")
	}
}
