package reminders

import (
	"context"
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestSetReminderChatToolErrors(t *testing.T) {
	tool := SetReminderChatTool("user-1", nil)
	res, err := tool.Handle(context.Background(), `{`)
	if err != nil || !strings.Contains(res.Content, "invalid arguments") {
		t.Fatalf("invalid json: %#v %v", res, err)
	}
	res, err = tool.Handle(context.Background(), `{"title":"x"}`)
	if err != nil || !strings.Contains(res.Content, "reminders_unavailable") {
		t.Fatalf("nil svc: %#v %v", res, err)
	}

	tool = SetReminderChatTool("", &Service{Store: &storage.RemindersStore{Enabled: true}})
	res, err = tool.Handle(context.Background(), `{"title":"x"}`)
	if err != nil || !strings.Contains(res.Content, "missing_user") {
		t.Fatalf("missing user: %#v %v", res, err)
	}

	tool = SetReminderChatTool("user-1", &Service{Store: &storage.RemindersStore{}})
	res, err = tool.Handle(context.Background(), `{"title":"x","timezone":"UTC"}`)
	if err != nil || !strings.Contains(res.Content, "Error:") {
		t.Fatalf("create error: %#v %v", res, err)
	}
}

func TestSetReminderChatToolCreates(t *testing.T) {
	store := testStore(t, nil)
	svc := &Service{Store: store}
	tool := SetReminderChatTool("user-1", svc)
	res, err := tool.Handle(context.Background(), `{"title":"Stretch","when":"in 10 minutes","timezone":"UTC"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Stretch") || !strings.Contains(res.Content, "Reminder set") {
		t.Fatalf("content: %s", res.Content)
	}

	res, err = tool.Handle(context.Background(), `{"title":"Water","timezone":"UTC"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Water") {
		t.Fatalf("default when: %s", res.Content)
	}
}
