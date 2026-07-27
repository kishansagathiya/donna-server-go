package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	calendarEventsURL   = "https://www.googleapis.com/calendar/v3/calendars/primary/events"
	calendarPrimaryURL  = "https://www.googleapis.com/calendar/v3/calendars/primary"
	defaultEventHour    = 9
	defaultEventMinutes = 0
)

type calendarEventRequest struct {
	Summary     string                `json:"summary"`
	Description string                `json:"description,omitempty"`
	Location    string                `json:"location,omitempty"`
	Start       calendarEventDateTime `json:"start"`
	End         calendarEventDateTime `json:"end"`
	Attendees   []calendarAttendee    `json:"attendees,omitempty"`
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

	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	tzName := firstNonEmpty(stringSlot(input, "timezone"), stringSlot(input, "time_zone"))
	if tzName == "" {
		if fetched, err := a.fetchPrimaryTimezone(ctx, client, accessToken); err == nil && fetched != "" {
			tzName = fetched
		} else {
			tzName = "UTC"
		}
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
		tzName = "UTC"
	}

	start, end, whenNote, err := resolveEventWindow(input, time.Now().In(loc), loc)
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

	attendees := parseAttendees(input)
	reqBody := calendarEventRequest{
		Summary:     title,
		Description: strings.Join(descParts, "\n\n"),
		Location:    stringSlot(input, "location"),
		Start: calendarEventDateTime{
			DateTime: start.In(loc).Format(time.RFC3339),
			TimeZone: tzName,
		},
		End: calendarEventDateTime{
			DateTime: end.In(loc).Format(time.RFC3339),
			TimeZone: tzName,
		},
		Attendees: attendees,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	endpoint := calendarEventsURL
	if len(attendees) > 0 {
		endpoint = calendarEventsURL + "?sendUpdates=all"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
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
		ID     string `json:"id"`
		HTML   string `json:"htmlLink"`
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
		"start":      start.In(loc).Format(time.RFC3339),
		"end":        end.In(loc).Format(time.RFC3339),
		"timezone":   tzName,
		"attendees":  attendeeEmails(attendees),
		"invites":    len(attendees) > 0,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"scheduled":  true,
	}, nil
}

func (a *Adapter) fetchPrimaryTimezone(ctx context.Context, client *http.Client, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, calendarPrimaryURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return "", mapGoogleAPIError("calendar_timezone", res.StatusCode, body)
	}
	var cal struct {
		TimeZone string `json:"timeZone"`
	}
	if err := json.Unmarshal(body, &cal); err != nil {
		return "", err
	}
	return strings.TrimSpace(cal.TimeZone), nil
}

func resolveEventWindow(input map[string]any, now time.Time, loc *time.Location) (start time.Time, end time.Time, whenNote string, err error) {
	if loc == nil {
		loc = time.UTC
	}
	now = now.In(loc)

	if raw := firstNonEmpty(stringSlot(input, "start"), stringSlot(input, "when")); raw != "" {
		if parsed, ok := parseFlexibleTime(raw, now, loc); ok {
			start = parsed
		} else {
			whenNote = raw
			start = nextHour(now)
		}
	} else {
		start = nextHour(now)
	}

	if rawEnd := stringSlot(input, "end"); rawEnd != "" {
		if parsed, ok := parseFlexibleTime(rawEnd, now, loc); ok && parsed.After(start) {
			end = parsed
		} else {
			end = start.Add(time.Hour)
		}
	} else if dur := stringSlot(input, "duration"); dur != "" {
		if d, ok := parseDurationHint(dur); ok {
			end = start.Add(d)
		} else {
			end = start.Add(time.Hour)
		}
	} else {
		end = start.Add(time.Hour)
	}
	return start, end, whenNote, nil
}

func nextHour(now time.Time) time.Time {
	return now.Truncate(time.Hour).Add(time.Hour)
}

func parseFlexibleTime(raw string, now time.Time, loc *time.Location) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	now = now.In(loc)

	// Absolute formats with zone/offset first.
	absoluteLayouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range absoluteLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.In(loc), true
		}
	}

	// Zone-less datetimes interpreted in the user's calendar timezone.
	zoneLessLayouts := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"01/02/2006 15:04",
		"2006-01-02",
		"01/02/2006",
	}
	for _, layout := range zoneLessLayouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			if layout == "2006-01-02" || layout == "01/02/2006" {
				return time.Date(t.Year(), t.Month(), t.Day(), defaultEventHour, defaultEventMinutes, 0, 0, loc), true
			}
			return t, true
		}
	}

	if t, ok := parseRelativeTime(raw, now, loc); ok {
		return t, true
	}
	return time.Time{}, false
}

var (
	reInDuration = regexp.MustCompile(`(?i)^in\s+(\d+)\s*(minutes?|mins?|hours?|hrs?|days?)$`)
	reAtClock    = regexp.MustCompile(`(?i)\b(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	reWeekday    = regexp.MustCompile(`(?i)\b(next\s+)?(sunday|monday|tuesday|wednesday|thursday|friday|saturday)\b`)
)

func parseRelativeTime(raw string, now time.Time, loc *time.Location) (time.Time, bool) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	lower = strings.Join(strings.Fields(lower), " ")
	if lower == "" {
		return time.Time{}, false
	}

	if m := reInDuration.FindStringSubmatch(lower); len(m) == 3 {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return time.Time{}, false
		}
		unit := m[2]
		switch {
		case strings.HasPrefix(unit, "min"):
			return now.Add(time.Duration(n) * time.Minute), true
		case strings.HasPrefix(unit, "hour") || strings.HasPrefix(unit, "hr"):
			return now.Add(time.Duration(n) * time.Hour), true
		case strings.HasPrefix(unit, "day"):
			return now.AddDate(0, 0, n), true
		}
	}

	day := now
	dayOffset := 0
	hasDay := false

	switch {
	case strings.Contains(lower, "day after tomorrow"):
		dayOffset = 2
		hasDay = true
	case strings.Contains(lower, "tomorrow"):
		dayOffset = 1
		hasDay = true
	case strings.Contains(lower, "today") || strings.Contains(lower, "tonight"):
		dayOffset = 0
		hasDay = true
	default:
		if m := reWeekday.FindStringSubmatch(lower); len(m) == 3 {
			target := weekdayFromName(m[2])
			if target >= 0 {
				dayOffset = daysUntilWeekday(now.Weekday(), time.Weekday(target), m[1] != "")
				hasDay = true
			}
		}
	}

	hour, minute, hasTime, ok := extractClock(lower)
	if !ok {
		return time.Time{}, false
	}
	if !hasTime {
		switch {
		case strings.Contains(lower, "tonight") || strings.Contains(lower, "evening"):
			hour, minute, hasTime = 18, 0, true
		case strings.Contains(lower, "afternoon"):
			hour, minute, hasTime = 15, 0, true
		case strings.Contains(lower, "morning"):
			hour, minute, hasTime = 9, 0, true
		case strings.Contains(lower, "noon"):
			hour, minute, hasTime = 12, 0, true
		case hasDay:
			hour, minute, hasTime = defaultEventHour, defaultEventMinutes, true
		}
	}
	if !hasDay && !hasTime {
		return time.Time{}, false
	}

	day = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, dayOffset)
	start := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)

	// Bare clock times ("at 3pm") with no day: if already past, use tomorrow.
	if !hasDay && hasTime && !start.After(now) {
		start = start.AddDate(0, 0, 1)
	}
	return start, true
}

func extractClock(lower string) (hour, minute int, found, ok bool) {
	m := reAtClock.FindStringSubmatch(lower)
	if len(m) == 0 {
		return 0, 0, false, true
	}
	h, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false, false
	}
	min := 0
	if m[2] != "" {
		min, err = strconv.Atoi(m[2])
		if err != nil || min > 59 {
			return 0, 0, false, false
		}
	}
	ampm := strings.ToLower(m[3])
	if ampm == "" {
		// 24h-style or bare hour without am/pm: treat 0-23 as-is when > 12,
		// otherwise leave as wall-clock hour (ambiguous 1-12 kept as-is).
		if h > 23 {
			return 0, 0, false, false
		}
		return h, min, true, true
	}
	if h < 1 || h > 12 {
		return 0, 0, false, false
	}
	if ampm == "pm" && h != 12 {
		h += 12
	}
	if ampm == "am" && h == 12 {
		h = 0
	}
	return h, min, true, true
}

func weekdayFromName(name string) int {
	switch strings.ToLower(name) {
	case "sunday":
		return int(time.Sunday)
	case "monday":
		return int(time.Monday)
	case "tuesday":
		return int(time.Tuesday)
	case "wednesday":
		return int(time.Wednesday)
	case "thursday":
		return int(time.Thursday)
	case "friday":
		return int(time.Friday)
	case "saturday":
		return int(time.Saturday)
	default:
		return -1
	}
}

func daysUntilWeekday(from time.Weekday, target time.Weekday, forceNext bool) int {
	delta := (int(target) - int(from) + 7) % 7
	if delta == 0 && forceNext {
		return 7
	}
	return delta
}

func parseDurationHint(raw string) (time.Duration, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.Join(strings.Fields(raw), " ")
	if m := reInDuration.FindStringSubmatch("in " + raw); len(m) == 3 {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return 0, false
		}
		switch {
		case strings.HasPrefix(m[2], "min"):
			return time.Duration(n) * time.Minute, true
		case strings.HasPrefix(m[2], "hour") || strings.HasPrefix(m[2], "hr"):
			return time.Duration(n) * time.Hour, true
		case strings.HasPrefix(m[2], "day"):
			return time.Duration(n) * 24 * time.Hour, true
		}
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d, true
	}
	return 0, false
}

func parseAttendees(input map[string]any) []calendarAttendee {
	raw := firstNonEmpty(
		stringSlot(input, "attendees"),
		stringSlot(input, "guests"),
		stringSlot(input, "invitees"),
		stringSlot(input, "recipient"),
		stringSlot(input, "to"),
	)
	if raw == "" {
		if list, ok := input["attendees"].([]any); ok {
			return attendeesFromList(list)
		}
		if list, ok := input["guests"].([]any); ok {
			return attendeesFromList(list)
		}
		return nil
	}
	return attendeesFromString(raw)
}

func attendeesFromList(list []any) []calendarAttendee {
	out := make([]calendarAttendee, 0, len(list))
	seen := map[string]bool{}
	for _, item := range list {
		switch v := item.(type) {
		case string:
			for _, a := range attendeesFromString(v) {
				if !seen[a.Email] {
					seen[a.Email] = true
					out = append(out, a)
				}
			}
		case map[string]any:
			email := strings.TrimSpace(strings.ToLower(fmt.Sprint(v["email"])))
			if strings.Contains(email, "@") && !seen[email] {
				seen[email] = true
				out = append(out, calendarAttendee{Email: email})
			}
		default:
			email := strings.TrimSpace(strings.ToLower(fmt.Sprint(item)))
			if strings.Contains(email, "@") && !seen[email] {
				seen[email] = true
				out = append(out, calendarAttendee{Email: email})
			}
		}
	}
	return out
}

func attendeesFromString(raw string) []calendarAttendee {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n'
	})
	out := make([]calendarAttendee, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		email := strings.TrimSpace(strings.ToLower(part))
		email = strings.Trim(email, "<>\"'")
		if strings.Contains(email, "@") && !seen[email] {
			seen[email] = true
			out = append(out, calendarAttendee{Email: email})
		}
	}
	return out
}

func attendeeEmails(attendees []calendarAttendee) []string {
	out := make([]string, 0, len(attendees))
	for _, a := range attendees {
		out = append(out, a.Email)
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
