package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const calendarEventsURL = "https://www.googleapis.com/calendar/v3/calendars/primary/events"

type calendarEventRequest struct {
	Summary     string               `json:"summary"`
	Description string               `json:"description,omitempty"`
	Location    string               `json:"location,omitempty"`
	Start       calendarEventDateTime `json:"start"`
	End         calendarEventDateTime `json:"end"`
	Attendees   []calendarAttendee   `json:"attendees,omitempty"`
}

type calendarEventDateTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type calendarAttendee struct {
	Email string `json:"email"`
}

func (a *Adapter) createEvent(ctx context.Context, accessToken string, input map[string]any) (map[string]any, error) {
	title := firstNonEmpty(
		stringSlot(input, "title"),
		stringSlot(input, "summary"),
	)
	if title == "" {
		return nil, fmt.Errorf("title_required")
	}

	start, end, whenNote, err := resolveEventWindow(input)
	if err != nil {
		return nil, err
	}

	descParts := make([]string, 0, 3)
	if notes := stringSlot(input, "notes"); notes != "" {
		descParts = append(descParts, notes)
	}
	if description := stringSlot(input, "description"); description != "" {
		descParts = append(descParts, description)
	}
	if whenNote != "" {
		descParts = append(descParts, "Original when: "+whenNote)
	}

	reqBody := calendarEventRequest{
		Summary:     title,
		Description: strings.Join(descParts, "\n\n"),
		Location:    stringSlot(input, "location"),
		Start: calendarEventDateTime{
			DateTime: start.UTC().Format(time.RFC3339),
			TimeZone: "UTC",
		},
		End: calendarEventDateTime{
			DateTime: end.UTC().Format(time.RFC3339),
			TimeZone: "UTC",
		},
		Attendees: parseAttendees(input),
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, calendarEventsURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return nil, mapGoogleAPIError("calendar_create", res.StatusCode, body)
	}

	var created struct {
		ID   string `json:"id"`
		HTML string `json:"htmlLink"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return nil, err
	}

	return map[string]any{
		"type":       "create_calendar_event",
		"provider":   "google",
		"event_id":   created.ID,
		"html_link":  created.HTML,
		"title":      title,
		"start":      start.UTC().Format(time.RFC3339),
		"end":        end.UTC().Format(time.RFC3339),
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"scheduled":  true,
	}, nil
}

func resolveEventWindow(input map[string]any) (start time.Time, end time.Time, whenNote string, err error) {
	now := time.Now().UTC()
	if raw := firstNonEmpty(stringSlot(input, "start"), stringSlot(input, "when")); raw != "" {
		if parsed, ok := parseFlexibleTime(raw, now); ok {
			start = parsed
		} else {
			whenNote = raw
			start = now.Truncate(time.Hour).Add(time.Hour)
		}
	} else {
		start = now.Truncate(time.Hour).Add(time.Hour)
	}

	if rawEnd := stringSlot(input, "end"); rawEnd != "" {
		if parsed, ok := parseFlexibleTime(rawEnd, now); ok && parsed.After(start) {
			end = parsed
		} else {
			end = start.Add(time.Hour)
		}
	} else {
		end = start.Add(time.Hour)
	}
	return start, end, whenNote, nil
}

func parseFlexibleTime(raw string, now time.Time) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"01/02/2006 15:04",
		"01/02/2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			if layout == "2006-01-02" || layout == "01/02/2006" {
				// Date-only → 9:00 local-as-UTC for a predictable default.
				return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, time.UTC), true
			}
			return t.UTC(), true
		}
	}
	_ = now
	return time.Time{}, false
}

func parseAttendees(input map[string]any) []calendarAttendee {
	raw := stringSlot(input, "attendees")
	if raw == "" {
		if list, ok := input["attendees"].([]any); ok {
			out := make([]calendarAttendee, 0, len(list))
			for _, item := range list {
				email := strings.TrimSpace(fmt.Sprint(item))
				if strings.Contains(email, "@") {
					out = append(out, calendarAttendee{Email: email})
				}
			}
			return out
		}
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	})
	out := make([]calendarAttendee, 0, len(parts))
	for _, part := range parts {
		email := strings.TrimSpace(part)
		if strings.Contains(email, "@") {
			out = append(out, calendarAttendee{Email: email})
		}
	}
	return out
}

func stringSlot(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	v, ok := input[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
