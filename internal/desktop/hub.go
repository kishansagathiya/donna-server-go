package desktop

import (
	"sync"
)

type Event struct {
	Kind     string         `json:"kind"`
	DeviceID string         `json:"device_id,omitempty"`
	Payload  map[string]any `json:"payload,omitempty"`
}

type subscriber struct {
	userID   string
	deviceID string
	ch       chan Event
}

// Hub fans control-plane events out to connected desktop workers.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]subscriber // deviceID -> chans
}

func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan Event]subscriber{}}
}

func (h *Hub) Subscribe(userID, deviceID string) (<-chan Event, func()) {
	if h == nil {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan Event, 16)
	h.mu.Lock()
	if h.subs[deviceID] == nil {
		h.subs[deviceID] = map[chan Event]subscriber{}
	}
	h.subs[deviceID][ch] = subscriber{userID: userID, deviceID: deviceID, ch: ch}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if m := h.subs[deviceID]; m != nil {
			delete(m, ch)
			if len(m) == 0 {
				delete(h.subs, deviceID)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
}

func (h *Hub) Publish(userID, deviceID, kind string, payload map[string]any) {
	if h == nil || deviceID == "" {
		return
	}
	ev := Event{Kind: kind, DeviceID: deviceID, Payload: payload}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch, sub := range h.subs[deviceID] {
		if userID != "" && sub.userID != userID {
			continue
		}
		select {
		case ch <- ev:
		default:
		}
	}
}
