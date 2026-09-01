package desktop

import (
	"testing"
)

func TestHubPublishToSubscriber(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("user-1", "dev-1")
	defer cancel()
	h.Publish("user-1", "dev-1", "run.available", map[string]any{"run_id": "r1"})
	ev := <-ch
	if ev.Kind != "run.available" {
		t.Fatalf("got %+v", ev)
	}
	h.Publish("other", "dev-1", "run.cancel", nil)
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event for other user: %+v", ev)
	default:
	}
}
