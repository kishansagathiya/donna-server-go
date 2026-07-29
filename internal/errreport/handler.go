package errreport

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxBodyBytes     = 16 << 10
	reportsPerMinute = 5
)

// errorPayload is the JSON contract clients POST to /errors.
type errorPayload struct {
	Source     string            `json:"source"`
	Message    string            `json:"message"`
	Stack      string            `json:"stack,omitempty"`
	Route      string            `json:"route,omitempty"`
	AppVersion string            `json:"appVersion,omitempty"`
	Context    map[string]string `json:"context,omitempty"`
}

// NewHandler serves POST /errors. It is intentionally public (no auth
// middleware): error reporting must keep working when auth itself is broken.
// Abuse is bounded by a per-IP rate limit and the Reporter's own global caps.
func NewHandler(r *Reporter) http.HandlerFunc {
	limiter := &ipLimiter{hits: map[string][]time.Time{}}
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !limiter.allow(clientIP(req), time.Now(), reportsPerMinute, time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":   "rate_limited",
				"message": "Too many error reports",
			})
			return
		}

		var body errorPayload
		dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBodyBytes))
		if err := dec.Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_body",
				"message": "Expected JSON body with source and message fields",
			})
			return
		}

		body.Source = strings.TrimSpace(body.Source)
		body.Message = strings.TrimSpace(body.Message)
		if body.Source != "web" && body.Source != "ios" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_source",
				"message": "source must be one of: web, ios",
			})
			return
		}
		if body.Message == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "missing_message",
				"message": "message is required",
			})
			return
		}

		r.Report(Report{
			Source:     body.Source,
			Message:    body.Message,
			Stack:      body.Stack,
			Route:      body.Route,
			AppVersion: body.AppVersion,
			Context:    body.Context,
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	}
}

func clientIP(r *http.Request) string {
	// chi's RealIP middleware has already resolved X-Forwarded-For into
	// RemoteAddr by the time this runs.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipLimiter is a fixed-window per-IP allowance (max events per window).
type ipLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *ipLimiter) allow(ip string, now time.Time, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-window)
	kept := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= max {
		l.hits[ip] = kept
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
