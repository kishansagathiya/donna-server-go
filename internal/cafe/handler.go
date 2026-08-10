package cafe

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	defaultTTL   = 45 * time.Second
	maxBodyBytes = 1 << 10
	maxClients   = 10_000
)

var clientIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// Handler tracks cafe visitors via lightweight HTTP heartbeats.
// In-memory is fine for a single Railway instance.
type Handler struct {
	mu       sync.Mutex
	presence map[string]time.Time
	ttl      time.Duration
}

func NewHandler() *Handler {
	return &Handler{
		presence: make(map[string]time.Time),
		ttl:      defaultTTL,
	}
}

type presenceBody struct {
	ClientID string `json:"clientId"`
}

// PostPresence registers a heartbeat and returns the live count.
func (h *Handler) PostPresence(w http.ResponseWriter, r *http.Request) {
	var body presenceBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&body); err != nil || !clientIDRe.MatchString(body.ClientID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}

	now := time.Now()
	h.mu.Lock()
	h.pruneLocked(now)
	if _, ok := h.presence[body.ClientID]; !ok && len(h.presence) >= maxClients {
		h.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "full"})
		return
	}
	h.presence[body.ClientID] = now
	count := len(h.presence)
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

// GetPresence returns the current online count (prunes stale entries).
func (h *Handler) GetPresence(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	h.mu.Lock()
	h.pruneLocked(now)
	count := len(h.presence)
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (h *Handler) pruneLocked(now time.Time) {
	for id, seen := range h.presence {
		if now.Sub(seen) > h.ttl {
			delete(h.presence, id)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// RegisterRoutes mounts public cafe presence endpoints (no auth).
func RegisterRoutes(r chi.Router, h *Handler) {
	r.Post("/cafe/presence", h.PostPresence)
	r.Get("/cafe/presence", h.GetPresence)
}
