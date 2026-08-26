package actions

import (
	"context"
	"testing"
)

func TestBuiltinDraftMessage(t *testing.T) {
	r := &BuiltinRunner{}
	out, err := r.Run(context.Background(), BuiltinDraftMessage, map[string]any{
		"recipient": "Mom",
		"body":      "Call about flights",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Output["sent"] != false {
		t.Fatalf("draft must not send: %#v", out.Output)
	}
	if out.Output["body"] != "Call about flights" {
		t.Fatalf("unexpected body: %#v", out.Output)
	}
}

func TestBuiltinProposeReminder(t *testing.T) {
	r := &BuiltinRunner{}
	out, err := r.Run(context.Background(), BuiltinProposeReminder, map[string]any{
		"title": "Call Mom",
		"when":  "Saturday",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Output["title"] != "Call Mom" {
		t.Fatalf("unexpected title: %#v", out.Output)
	}
	// Persistence happens in Executor via ReminderEffects, not the dry-run builtin.
	if out.Output["scheduled"] != false {
		t.Fatalf("builtin dry-run must not claim scheduled: %#v", out.Output)
	}
}

func TestBuiltinOpenURL(t *testing.T) {
	r := &BuiltinRunner{}
	out, err := r.Run(context.Background(), BuiltinOpenURL, map[string]any{
		"url": "https://example.com/path",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Output["url"] != "https://example.com/path" {
		t.Fatalf("unexpected url: %#v", out.Output)
	}
	if out.Output["opened"] != false {
		t.Fatalf("open_url must not auto-open: %#v", out.Output)
	}
}

func TestBuiltinOpenURLRejectsBadScheme(t *testing.T) {
	r := &BuiltinRunner{}
	if _, err := r.Run(context.Background(), BuiltinOpenURL, map[string]any{
		"url": "javascript:alert(1)",
	}); err == nil {
		t.Fatal("expected error for javascript URL")
	}
}

func TestNoCreateNoteBuiltin(t *testing.T) {
	r := &BuiltinRunner{}
	if _, err := r.Run(context.Background(), BuiltinName("create_note"), map[string]any{
		"content": "should not work",
	}); err == nil {
		t.Fatal("create_note must not be a builtin")
	}
}

func TestBuiltinFromConfigAllowsIntegrationBuiltins(t *testing.T) {
	for _, name := range []string{"create_calendar_event", "send_email", "draft_message"} {
		got, err := BuiltinFromConfig([]byte(`{"builtin":"` + name + `"}`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != name {
			t.Fatalf("got %s want %s", got, name)
		}
	}
	if _, err := BuiltinFromConfig([]byte(`{"builtin":"create_note"}`)); err == nil {
		t.Fatal("create_note must remain rejected")
	}
}
