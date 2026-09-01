package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

func (h *Handler) Runner(w http.ResponseWriter, r *http.Request) {
	userID, device, ok := h.deviceContext(w, r)
	if !ok {
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Warn("desktop runner accept failed", map[string]any{"error": err.Error()})
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")

	if h.Hub == nil {
		h.Hub = NewHub()
	}
	events, cancel := h.Hub.Subscribe(userID, device.ID)
	defer cancel()

	ctx := r.Context()
	_ = h.writeEvent(ctx, conn, Event{
		Kind:     "hello",
		DeviceID: device.ID,
		Payload:  map[string]any{"device_id": device.ID},
	})

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(writeCtx)
			cancelWrite()
			if err != nil {
				return
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := h.writeEvent(ctx, conn, ev); err != nil {
				return
			}
		}
	}
}

func (h *Handler) writeEvent(ctx context.Context, conn *websocket.Conn, ev Event) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, raw)
}
