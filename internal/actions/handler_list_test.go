package actions

import (
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestApplyActionMeta(t *testing.T) {
	run := storage.ActionRun{ID: "run-1", ActionID: "act-1"}
	got := applyActionMeta(run, storage.Action{
		ID:   "act-1",
		Slug: "create_calendar_event",
		Name: "Create calendar event",
		Risk: "external",
	})
	if got.ActionSlug == nil || *got.ActionSlug != "create_calendar_event" {
		t.Fatalf("slug: %#v", got.ActionSlug)
	}
	if got.ActionName == nil || *got.ActionName != "Create calendar event" {
		t.Fatalf("name: %#v", got.ActionName)
	}
	if got.ActionRisk == nil || *got.ActionRisk != "external" {
		t.Fatalf("risk: %#v", got.ActionRisk)
	}
}

func TestApplyActionMetaEmpty(t *testing.T) {
	run := storage.ActionRun{ID: "run-1", ActionID: "act-1"}
	got := applyActionMeta(run, storage.Action{})
	if got.ActionSlug != nil {
		t.Fatalf("expected no slug, got %#v", got.ActionSlug)
	}
}

func TestIsIntegrationBuiltin(t *testing.T) {
	if !IsIntegrationBuiltin(BuiltinCreateCalendarEvent) {
		t.Fatal("create_calendar_event should be integration builtin")
	}
	if !IsIntegrationBuiltin(BuiltinSendEmail) {
		t.Fatal("send_email should be integration builtin")
	}
	if IsIntegrationBuiltin(BuiltinDraftMessage) {
		t.Fatal("draft_message should not be integration builtin")
	}
}
