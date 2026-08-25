package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/memory"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Phase3Tools are optional commerce / write-back tools (booking + cron follow-through).
type Phase3Tools struct {
	Facts    FactWriter
	Calendar CalendarProposer
}

// FactWriter persists a user-approved memory fact from an agent run.
type FactWriter interface {
	InsertFactReturning(ctx context.Context, userID string, input storage.NewFactInput) (storage.KbFact, error)
}

// CalendarEventProposal is a proposed Google Calendar write (user confirms in Actions).
type CalendarEventProposal struct {
	Title     string
	When      string
	Start     string
	End       string
	Location  string
	Notes     string
	Attendees string
	Timezone  string
}

// CalendarProposer creates a proposed create_calendar_event action_run.
type CalendarProposer interface {
	Propose(ctx context.Context, userID, agentRunID string, event CalendarEventProposal) (actionRunID string, intentID string, err error)
}

// ActionsCalendarProposer implements CalendarProposer via intents + action_runs.
type ActionsCalendarProposer struct {
	Store *storage.ActionsStore
}

func (p *ActionsCalendarProposer) Propose(ctx context.Context, userID, agentRunID string, event CalendarEventProposal) (string, string, error) {
	if p == nil || p.Store == nil || !p.Store.Enabled {
		return "", "", fmt.Errorf("actions_unavailable")
	}
	title := strings.TrimSpace(event.Title)
	if title == "" {
		return "", "", fmt.Errorf("title_required")
	}
	action, err := p.Store.GetSystemActionBySlug(ctx, "create_calendar_event")
	if err != nil {
		return "", "", err
	}
	slots := map[string]any{"title": title}
	if v := strings.TrimSpace(event.When); v != "" {
		slots["when"] = v
	}
	if v := strings.TrimSpace(event.Start); v != "" {
		slots["start"] = v
		if _, ok := slots["when"]; !ok {
			slots["when"] = v
		}
	}
	if v := strings.TrimSpace(event.End); v != "" {
		slots["end"] = v
	}
	if v := strings.TrimSpace(event.Location); v != "" {
		slots["location"] = v
	}
	if v := strings.TrimSpace(event.Notes); v != "" {
		slots["notes"] = v
	}
	if v := strings.TrimSpace(event.Attendees); v != "" {
		slots["attendees"] = v
	}
	if v := strings.TrimSpace(event.Timezone); v != "" {
		slots["timezone"] = v
	}
	slotsJSON, err := json.Marshal(slots)
	if err != nil {
		return "", "", err
	}
	conf := 0.95
	sourceID := strings.TrimSpace(agentRunID)
	var sourcePtr *string
	if sourceID != "" {
		sourcePtr = &sourceID
	}
	intent, err := p.Store.CreateIntent(ctx, userID, storage.NewIntentInput{
		Kind:       "create_calendar_event",
		Summary:    title,
		Slots:      slotsJSON,
		SourceType: "agent_run",
		SourceID:   sourcePtr,
		Confidence: &conf,
	})
	if err != nil {
		return "", "", err
	}
	input := map[string]any{}
	for k, v := range slots {
		input[k] = v
	}
	input["summary"] = title
	input["intent_kind"] = "create_calendar_event"
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return "", "", err
	}
	intentID := intent.ID
	run, err := p.Store.CreateActionRun(ctx, userID, storage.NewActionRunInput{
		IntentID: &intentID,
		ActionID: action.ID,
		Status:   "proposed",
		Input:    inputJSON,
	})
	if err != nil {
		return "", "", err
	}
	return run.ID, intent.ID, nil
}

var longDigitRun = regexp.MustCompile(`\d{13,19}`)

func searchFlightsTool() RegisteredTool {
	return RegisteredTool{
		Toolset: "commerce",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "search_flights",
				Description: "Look up flights for origin/destination/dates. Live partner APIs are optional; if unconfigured this tool tells you to research with fetch_url/browse_page and then request_approval with a structured itinerary. Never invent prices or treat this as a booking confirmation.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"origin":      map[string]any{"type": "string", "description": "IATA or city, e.g. SFO"},
						"destination": map[string]any{"type": "string", "description": "IATA or city, e.g. LIS"},
						"depart_date": map[string]any{"type": "string", "description": "YYYY-MM-DD"},
						"return_date": map[string]any{"type": "string", "description": "YYYY-MM-DD if round trip"},
						"passengers":  map[string]any{"type": "integer"},
						"cabin":       map[string]any{"type": "string", "description": "economy, premium, business"},
					},
					"required": []string{"origin", "destination", "depart_date"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Origin      string `json:"origin"`
				Destination string `json:"destination"`
				DepartDate  string `json:"depart_date"`
				ReturnDate  string `json:"return_date"`
				Passengers  int    `json:"passengers"`
				Cabin       string `json:"cabin"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			origin := strings.ToUpper(strings.TrimSpace(args.Origin))
			dest := strings.ToUpper(strings.TrimSpace(args.Destination))
			depart := strings.TrimSpace(args.DepartDate)
			if origin == "" || dest == "" || depart == "" {
				return ToolResult{Content: "Error: origin, destination, and depart_date are required."}, nil
			}
			passengers := args.Passengers
			if passengers <= 0 {
				passengers = 1
			}
			cabin := strings.TrimSpace(args.Cabin)
			if cabin == "" {
				cabin = "economy"
			}
			msg := strings.TrimSpace(fmt.Sprintf(`No live flight-search partner is configured. Do not invent fares or availability.

Research this itinerary with fetch_url and browse_page (Google Flights, airline sites, or the user's preferred OTA), then call request_approval with:
- kind: book_flight
- summary: one sentence of the option
- details.itinerary, details.total, details.currency, details.airline, details.dates, details.passengers, details.source_url

Query: %s → %s depart %s%s · %d passenger(s) · %s

Never collect or store card numbers. Booking is not complete until the user taps Confirm.`,
				origin, dest, depart, returnSuffix(args.ReturnDate), passengers, cabin))
			return ToolResult{
				Content: msg,
				Meta: map[string]any{
					"provider":    "unconfigured",
					"origin":      origin,
					"destination": dest,
					"depart_date": depart,
					"return_date": strings.TrimSpace(args.ReturnDate),
					"passengers":  passengers,
					"cabin":       cabin,
				},
			}, nil
		},
	}
}

func returnSuffix(returnDate string) string {
	if strings.TrimSpace(returnDate) == "" {
		return ""
	}
	return " return " + strings.TrimSpace(returnDate)
}

func writeMemoryFactTool(writer FactWriter) RegisteredTool {
	return RegisteredTool{
		Toolset: "commerce",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "write_memory_fact",
				Description: "Save a durable personal fact (travel prefs, people, routines). Never store card numbers, CVV, passwords, or checkout tokens. Skip one-off prices.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"fact":        map[string]any{"type": "string"},
						"entity_name": map[string]any{"type": "string"},
						"topic":       map[string]any{"type": "string", "description": "e.g. travel, booking"},
					},
					"required": []string{"fact"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Fact       string `json:"fact"`
				EntityName string `json:"entity_name"`
				Topic      string `json:"topic"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			fact := strings.TrimSpace(args.Fact)
			if fact == "" {
				return ToolResult{Content: "Error: fact is required."}, nil
			}
			if len(fact) > 2000 {
				return ToolResult{Content: "Error: fact_too_long"}, nil
			}
			if longDigitRun.MatchString(fact) {
				return ToolResult{Content: "Refused: looks like a card or account number. Donna never stores payment data."}, nil
			}
			cand := memory.Candidate{Fact: fact, Explicit: true, Confidence: 1}
			if reason := memory.RejectUnsafe(cand); reason != memory.RejectNone {
				return ToolResult{Content: "Refused: " + string(reason) + ". Do not store secrets or payment data."}, nil
			}
			if writer == nil {
				return ToolResult{Content: "Error: memory_unavailable"}, nil
			}
			if runCtx == nil || strings.TrimSpace(runCtx.UserID) == "" {
				return ToolResult{Content: "Error: missing_user"}, nil
			}
			in := storage.NewFactInput{Fact: fact}
			if v := strings.TrimSpace(args.EntityName); v != "" {
				in.EntityName = &v
			}
			topic := strings.TrimSpace(args.Topic)
			if topic == "" {
				topic = "agent"
			}
			in.Topic = &topic
			row, err := writer.InsertFactReturning(ctx, runCtx.UserID, in)
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			return ToolResult{
				Content: "Saved memory fact " + row.ID + ": " + fact,
				Meta:    map[string]any{"fact_id": row.ID, "fact": fact},
			}, nil
		},
	}
}

func proposeCalendarEventTool(proposer CalendarProposer) RegisteredTool {
	return RegisteredTool{
		Toolset: "commerce",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "propose_calendar_event",
				Description: "Create a proposed Google Calendar event the user confirms in Actions. Does not write the calendar until they confirm. Use after an approved booking or when the goal is to put something on the calendar.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":     map[string]any{"type": "string"},
						"when":      map[string]any{"type": "string", "description": "Human or ISO datetime for start"},
						"start":     map[string]any{"type": "string"},
						"end":       map[string]any{"type": "string"},
						"location":  map[string]any{"type": "string"},
						"notes":     map[string]any{"type": "string"},
						"attendees": map[string]any{"type": "string"},
						"timezone":  map[string]any{"type": "string"},
					},
					"required": []string{"title"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Title     string `json:"title"`
				When      string `json:"when"`
				Start     string `json:"start"`
				End       string `json:"end"`
				Location  string `json:"location"`
				Notes     string `json:"notes"`
				Attendees string `json:"attendees"`
				Timezone  string `json:"timezone"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			if proposer == nil {
				return ToolResult{Content: "Error: calendar_unavailable"}, nil
			}
			if runCtx == nil || strings.TrimSpace(runCtx.UserID) == "" {
				return ToolResult{Content: "Error: missing_user"}, nil
			}
			runID := ""
			if runCtx != nil {
				runID = runCtx.RunID
			}
			actionRunID, intentID, err := proposer.Propose(ctx, runCtx.UserID, runID, CalendarEventProposal{
				Title:     args.Title,
				When:      args.When,
				Start:     args.Start,
				End:       args.End,
				Location:  args.Location,
				Notes:     args.Notes,
				Attendees: args.Attendees,
				Timezone:  args.Timezone,
			})
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			return ToolResult{
				Content: fmt.Sprintf("Proposed calendar event %q. The user must confirm it in Actions before Google Calendar is written. action_run=%s intent=%s", strings.TrimSpace(args.Title), actionRunID, intentID),
				Meta: map[string]any{
					"action_run_id": actionRunID,
					"intent_id":     intentID,
					"title":         strings.TrimSpace(args.Title),
				},
			}, nil
		},
	}
}
