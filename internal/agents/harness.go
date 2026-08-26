package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	DefaultMaxSteps  = 80
	DefaultWallClock = 20 * time.Minute
	DefaultLease     = 2 * time.Minute
	DefaultMaxTokens = 24_000 // approximate chars/4 budget for working transcript
	compressKeepHead = 2
	compressKeepTail = 8
)

// Completer is the LLM surface the harness needs (mockable in tests).
type Completer interface {
	CompleteOnceWithOptions(ctx context.Context, messages []providers.ChatMessage, options providers.ChatCompletionOptions) (providers.ChatCompletionMetadata, error)
}

// MemorySearcher retrieves user memory for a query.
type MemorySearcher interface {
	Search(ctx context.Context, userID, query string, limit int) ([]MemoryHit, error)
}

type MemoryHit struct {
	Source string  `json:"source"`
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	Score  float64 `json:"score"`
}

// NoteSearcher searches user notes.
type NoteSearcher interface {
	Search(ctx context.Context, userID, query string, limit int) ([]NoteHit, error)
}

type NoteHit struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Preview string `json:"preview"`
}

// Budgets caps a single agent run.
type Budgets struct {
	MaxSteps  int
	WallClock time.Duration
	Lease     time.Duration
}

func (b Budgets) withDefaults() Budgets {
	if b.MaxSteps <= 0 {
		b.MaxSteps = DefaultMaxSteps
	}
	if b.WallClock <= 0 {
		b.WallClock = DefaultWallClock
	}
	if b.Lease <= 0 {
		b.Lease = DefaultLease
	}
	return b
}

// Harness is the Hermes-shaped agent loop.
type Harness struct {
	Store     RunStore
	LLM       Completer
	Registry  *Registry
	WorkerID  string
	Budgets   Budgets
	System    string // optional override system prompt prefix
	Approvals ApprovalRecorder
	Browser   SessionCloser
}

// Run executes (or resumes) one agent_run until terminal, waiting_for_user, or cancel.
func (h *Harness) Run(ctx context.Context, run storage.AgentRun) error {
	return h.run(ctx, run)
}

func (h *Harness) run(ctx context.Context, run storage.AgentRun) error {
	if h == nil || h.Store == nil {
		return fmt.Errorf("agents_disabled")
	}
	if h.LLM == nil {
		return fmt.Errorf("llm_required")
	}
	if h.Registry == nil || h.Registry.Len() == 0 {
		return fmt.Errorf("tools_required")
	}
	budgets := h.Budgets.withDefaults()
	if run.MaxSteps > 0 {
		budgets.MaxSteps = run.MaxSteps
	}
	workerID := h.WorkerID
	if workerID == "" {
		workerID = "donna-agent"
	}

	deadline := time.Now().Add(budgets.WallClock)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer h.closeBrowserIfTerminal(context.Background(), run)

	run, err := h.Store.Heartbeat(runCtx, run.UserID, run.ID, workerID, budgets.Lease)
	if err != nil {
		return err
	}

	messages, plan, seq, err := h.bootstrapMessages(runCtx, run)
	if err != nil {
		return h.fail(runCtx, run, seq, err.Error())
	}
	seq++
	if _, err := h.Store.AppendStep(runCtx, run.UserID, run.ID, seq, storage.AgentStepStatus, map[string]any{"text": "started"}); err != nil {
		return err
	}

	rc := &RunContext{
		UserID: run.UserID,
		RunID:  run.ID,
		Goal:   run.Goal,
		Plan:   plan,
		SetPlan: func(items []TodoItem) {
			plan = items
		},
		Extra: map[string]any{"store": h.Store},
	}
	if run.EmployeeID != nil {
		rc.EmployeeID = *run.EmployeeID
	}

	for step := 0; step < budgets.MaxSteps; step++ {
		if err := runCtx.Err(); err != nil {
			return h.fail(runCtx, run, seq, "context_cancelled")
		}
		if time.Now().After(deadline) {
			return h.fail(runCtx, run, seq, "wall_clock_exceeded")
		}

		// Refresh cancel / redirect from store.
		fresh, err := h.Store.Get(runCtx, run.UserID, run.ID)
		if err != nil {
			return err
		}
		if fresh.Status == storage.AgentStatusCancelled ||
			fresh.Status == storage.AgentStatusSucceeded ||
			fresh.Status == storage.AgentStatusFailed ||
			fresh.Status == storage.AgentStatusExpired {
			return nil
		}
		if fresh.RedirectPending != nil && strings.TrimSpace(*fresh.RedirectPending) != "" {
			msg := strings.TrimSpace(*fresh.RedirectPending)
			seq++
			if _, err := h.Store.AppendStep(runCtx, run.UserID, run.ID, seq, storage.AgentStepUserMessage, map[string]any{
				"message": msg,
				"kind":    "redirect",
			}); err != nil {
				return err
			}
			messages = append(messages, providers.ChatMessage{Role: "user", Content: "User redirect: " + msg})
			_, _ = h.Store.Patch(runCtx, run.UserID, run.ID, map[string]any{"redirect_pending": nil})
		}

		if _, err := h.Store.Heartbeat(runCtx, run.UserID, run.ID, workerID, budgets.Lease); err != nil {
			return err
		}

		messages = compressIfNeeded(messages, run.Goal)

		defs := h.Registry.Definitions(run.ToolAllowlist)
		meta, err := h.LLM.CompleteOnceWithOptions(runCtx, messages, providers.ChatCompletionOptions{
			Tools:      defs,
			ToolChoice: "auto",
		})
		if err != nil {
			log.Warn("agent llm failed", map[string]any{"runId": log.ShortID(run.ID), "error": err.Error()})
			return h.fail(runCtx, run, seq, "llm_error: "+err.Error())
		}

		closed, err := h.closed(runCtx, run)
		if err != nil {
			return err
		}
		if closed {
			return nil
		}

		if len(meta.ToolCalls) == 0 {
			content := strings.TrimSpace(meta.Content)
			if content == "" {
				content = "Done."
			}
			seq++
			if _, err := h.Store.AppendStep(runCtx, run.UserID, run.ID, seq, storage.AgentStepThought, map[string]any{
				"text": content,
			}); err != nil {
				return err
			}
			if looksLikeClarifyingQuestion(content) {
				payload := map[string]any{
					"kind":     "ask_user",
					"question": content,
					"summary":  content,
					"plan":     plan,
				}
				if opts := extractOptionsFromText(content); len(opts) > 0 {
					payload["options"] = opts
				}
				seq++
				if _, err := h.Store.AppendStep(runCtx, run.UserID, run.ID, seq, storage.AgentStepApprovalRequest, payload); err != nil {
					return err
				}
				_, err := h.Store.WaitForUser(runCtx, run.UserID, run.ID, payload)
				return err
			}
			return h.succeed(runCtx, run, map[string]any{
				"summary": content,
				"plan":    plan,
			})
		}

		assistant := providers.ChatMessage{
			Role:      "assistant",
			Content:   meta.Content,
			ToolCalls: meta.ToolCalls,
		}
		messages = append(messages, assistant)
		if strings.TrimSpace(meta.Content) != "" {
			seq++
			if _, err := h.Store.AppendStep(runCtx, run.UserID, run.ID, seq, storage.AgentStepThought, map[string]any{
				"text": meta.Content,
			}); err != nil {
				return err
			}
		}

		for _, call := range meta.ToolCalls {
			name := call.Function.Name
			args := call.Function.Arguments
			seq++
			if _, err := h.Store.AppendStep(runCtx, run.UserID, run.ID, seq, storage.AgentStepToolCall, map[string]any{
				"id":   call.ID,
				"name": name,
				"args": jsonRawOrString(args),
			}); err != nil {
				return err
			}

			if name == "request_approval" || name == "ask_user" {
				argsMap := map[string]any{}
				if m, ok := jsonRawOrString(args).(map[string]any); ok {
					argsMap = m
				}
				question := strings.TrimSpace(fmt.Sprint(argsMap["question"]))
				if question == "" || question == "<nil>" {
					question = strings.TrimSpace(fmt.Sprint(argsMap["summary"]))
				}
				if question == "" || question == "<nil>" {
					question = strings.TrimSpace(meta.Content)
				}
				if question == "" {
					question = "Donna needs your input to continue."
				}
				payload := map[string]any{
					"kind":     name,
					"tool":     name,
					"question": question,
					"summary":  question,
					"args":     argsMap,
				}
				if ctx := strings.TrimSpace(fmt.Sprint(argsMap["context"])); ctx != "" && ctx != "<nil>" {
					payload["context"] = ctx
				}
				if opts := normalizeAskOptions(argsMap["options"]); len(opts) > 0 {
					payload["options"] = opts
				}
				if allowMulti, ok := argsMap["allow_multiple"].(bool); ok && allowMulti {
					payload["allow_multiple"] = true
				}
				if details, ok := argsMap["details"]; ok && details != nil {
					payload["details"] = details
				}
				if name == "request_approval" && h.Approvals != nil {
					if id, err := h.Approvals.RecordRequest(runCtx, run.UserID, run.ID, payload); err != nil {
						logApprovalLedger(err, run.ID)
					} else if id != "" {
						payload["action_run_id"] = id
					}
				}
				seq++
				if _, err := h.Store.AppendStep(runCtx, run.UserID, run.ID, seq, storage.AgentStepApprovalRequest, payload); err != nil {
					return err
				}
				_, err := h.Store.WaitForUser(runCtx, run.UserID, run.ID, payload)
				return err
			}

			tool, ok := h.Registry.Get(name)
			var result ToolResult
			var toolErr error
			if !ok {
				result = ToolResult{Content: "Error: unknown tool " + name}
			} else {
				rc.Plan = plan
				result, toolErr = tool.Handle(runCtx, rc, args)
				plan = rc.Plan
				if toolErr != nil {
					result = ToolResult{Content: "Error: " + toolErr.Error()}
				}
			}

			seq++
			stepPayload := map[string]any{
				"id":      call.ID,
				"name":    name,
				"content": truncate(result.Content, 8_000),
			}
			if result.Meta != nil {
				stepPayload["meta"] = result.Meta
			}
			if _, err := h.Store.AppendStep(runCtx, run.UserID, run.ID, seq, storage.AgentStepToolResult, stepPayload); err != nil {
				return err
			}

			messages = append(messages, providers.ChatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       name,
				Content:    result.Content,
			})

			if result.Finish {
				out := map[string]any{
					"summary": result.Content,
					"plan":    plan,
				}
				for k, v := range result.FinishResult {
					out[k] = v
				}
				return h.succeed(runCtx, run, out)
			}
		}

		// Persist plan if updated.
		if len(plan) > 0 {
			raw, _ := json.Marshal(plan)
			_, _ = h.Store.Patch(runCtx, run.UserID, run.ID, map[string]any{"plan": json.RawMessage(raw)})
		}
	}

	return h.fail(runCtx, run, seq, "max_steps_exceeded")
}

func (h *Harness) bootstrapMessages(ctx context.Context, run storage.AgentRun) ([]providers.ChatMessage, []TodoItem, int, error) {
	sys := h.System
	if strings.TrimSpace(sys) == "" {
		sys = defaultSystemPrompt()
	}
	memBlock := ""
	groundedGoal := run.Goal
	skillsBlock := ""
	if len(run.MemorySnapshot) > 0 && string(run.MemorySnapshot) != "{}" && string(run.MemorySnapshot) != "null" {
		var snap map[string]any
		if err := json.Unmarshal(run.MemorySnapshot, &snap); err == nil {
			if g, ok := snap["grounded_goal"].(string); ok && strings.TrimSpace(g) != "" {
				groundedGoal = strings.TrimSpace(g)
			}
			// Keep hits in the system prompt without dumping grounded_goal twice.
			if hits, ok := snap["hits"]; ok {
				rawHits, _ := json.Marshal(map[string]any{"hits": hits})
				memBlock = "\n\nMemory snapshot:\n" + string(rawHits)
			}
			skillsBlock = renderSkillsBlock(snap["skills"])
		} else {
			memBlock = "\n\nMemory snapshot:\n" + string(run.MemorySnapshot)
		}
	}
	var plan []TodoItem
	_ = json.Unmarshal(run.Plan, &plan)

	steps, err := h.Store.ListSteps(ctx, run.UserID, run.ID, 0, 500)
	if err != nil {
		return nil, nil, 0, err
	}
	seq := 0
	for _, st := range steps {
		if st.Seq > seq {
			seq = st.Seq
		}
	}

	messages := []providers.ChatMessage{
		{Role: "system", Content: sys + memBlock + skillsBlock},
		{Role: "user", Content: "Goal: " + groundedGoal},
	}

	// Rebuild a compact transcript from prior steps for resume.
	for _, st := range steps {
		switch st.Kind {
		case storage.AgentStepUserMessage:
			var p struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(st.Payload, &p)
			if p.Message != "" {
				messages = append(messages, providers.ChatMessage{Role: "user", Content: "User redirect: " + p.Message})
			}
		case storage.AgentStepThought:
			var p struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(st.Payload, &p)
			if p.Text != "" {
				messages = append(messages, providers.ChatMessage{Role: "assistant", Content: p.Text})
			}
		case storage.AgentStepToolResult:
			var p struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Content string `json:"content"`
			}
			_ = json.Unmarshal(st.Payload, &p)
			if p.Content != "" {
				messages = append(messages, providers.ChatMessage{
					Role:       "tool",
					ToolCallID: p.ID,
					Name:       p.Name,
					Content:    p.Content,
				})
			}
		}
	}
	return messages, plan, seq, nil
}

func (h *Harness) closed(ctx context.Context, run storage.AgentRun) (bool, error) {
	fresh, err := h.Store.Get(ctx, run.UserID, run.ID)
	if err != nil {
		return false, err
	}
	return storage.IsTerminalAgentStatus(fresh.Status), nil
}

func (h *Harness) succeed(ctx context.Context, run storage.AgentRun, result map[string]any) error {
	closed, err := h.closed(ctx, run)
	if err != nil || closed {
		return err
	}
	_, err = h.Store.Finish(ctx, run.UserID, run.ID, storage.AgentStatusSucceeded, result, "")
	return err
}

func (h *Harness) fail(ctx context.Context, run storage.AgentRun, seq int, errText string) error {
	closed, err := h.closed(ctx, run)
	if err != nil || closed {
		return err
	}
	_, _ = h.Store.AppendStep(ctx, run.UserID, run.ID, seq+1, storage.AgentStepError, map[string]any{"error": errText})
	_, err = h.Store.Finish(ctx, run.UserID, run.ID, storage.AgentStatusFailed, nil, errText)
	return err
}

func (h *Harness) closeBrowserIfTerminal(ctx context.Context, run storage.AgentRun) {
	if h == nil || h.Browser == nil || strings.TrimSpace(run.ID) == "" {
		return
	}
	fresh := run
	if h.Store != nil {
		if got, err := h.Store.Get(ctx, run.UserID, run.ID); err == nil {
			fresh = got
		}
	}
	if storage.IsTerminalAgentStatus(fresh.Status) {
		_ = h.Browser.CloseSession(ctx, run.ID)
	}
}

func defaultSystemPrompt() string {
	return strings.TrimSpace(`You are Donna's cloud agent harness — a long-running personal assistant worker.
You run on Donna's servers while the user's phone may be locked.

Rules:
- Pursue the user's goal thoroughly using tools. Do not ask them to keep the app open.
- Prefer memory_search / search_notes before external fetch when the answer may be personal.
- Use fetch_url for static HTML/docs. Use browse_page for a one-shot extract of a JS page. For forms or multi-step sites, use browser_navigate then browser_snapshot then browser_click / browser_type on the same session. Never type card numbers, CVV, or passwords. Never click Pay / Place order / Submit payment — call request_approval first.
- Use fetch_image when you have a direct public image URL (jpeg/png/gif/webp) and should show it. After it succeeds, include the returned markdown image ![description](url) on its own line in the user-visible summary. Never invent image URLs. You cannot generate images.
- Keep a short todo plan via the todo tool for multi-step goals.
- Use delegate_task for parallel research workstreams. Children cannot delegate or book. Prefer wait=true.
- Skills listed in the system prompt may help: call load_skill(name) to get a skill's full instructions and follow them when they apply. User-selected skills are already included in full — follow them.
- When you need more information from the user, call ask_user with a clear question and stop. Never ask a clarifying question as your final plain-text reply — they can only answer through the Reply UI after ask_user.
- Whenever the answer is one of a few discrete choices (airports, dates, yes/no, airlines, seat prefs, which note/photo), include an options array with short labels. Set allow_multiple true only when they may pick more than one. Prefer taps over typing.
- When you need irreversible approval (pay, book, send), call request_approval and stop. For flights/hotels/purchases, fill details with itinerary, total, currency, airline/vendor, dates, and source_url. Never invent prices or a completed booking.
- search_flights has no live partner until configured — treat an unconfigured result as a research hint, then fetch_url/browse_page, then request_approval. Never treat tool output as a paid ticket.
- Never store payment card numbers, CVV, passwords, or checkout tokens in memory, skills, or approval details. Donna cannot charge a card.
- After the user approves a booking, call propose_calendar_event, write_memory_fact for durable prefs (not fares), and save_skill if the procedure is reusable. Calendar writes still need a separate Confirm in Actions.
- On AI employee shifts: call report_progress before wrapping up; call complete_goal only when the ongoing goal is fully achieved.
- When the goal is complete, reply with a clear final summary and no further tool calls.
- Never invent confirmations, bookings, or private facts not grounded in tool results.`)
}

// renderSkillsBlock renders the snapshot skills section for the system prompt.
// Selected skills (user picked) are injected in full; matched skills are
// listed name+description for progressive disclosure via load_skill.
func renderSkillsBlock(raw any) string {
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	var selected strings.Builder
	var matched strings.Builder
	for _, item := range arr {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		desc, _ := entry["description"].(string)
		kind, _ := entry["kind"].(string)
		if kind == "selected" {
			selected.WriteString("\n\n### Skill: " + name)
			if strings.TrimSpace(desc) != "" {
				selected.WriteString("\n" + desc)
			}
			if content, _ := entry["content"].(string); strings.TrimSpace(content) != "" {
				selected.WriteString("\n\n" + strings.TrimSpace(content))
			}
			continue
		}
		matched.WriteString("- " + name)
		if strings.TrimSpace(desc) != "" {
			matched.WriteString(" — " + desc)
		}
		matched.WriteString("\n")
	}
	var b strings.Builder
	if selected.Len() > 0 {
		b.WriteString("\n\nUser-selected skills — follow these procedures:\n" + strings.TrimSpace(selected.String()))
	}
	if matched.Len() > 0 {
		b.WriteString("\n\nMatching skills (call load_skill(name) for full instructions before relying on one):\n")
		b.WriteString(strings.TrimSuffix(matched.String(), "\n"))
	}
	return b.String()
}

// normalizeAskOptions coerces tool args into [{id,label}, ...].
func normalizeAskOptions(raw any) []map[string]string {
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]map[string]string, 0, len(arr))
	for i, item := range arr {
		switch v := item.(type) {
		case string:
			label := strings.TrimSpace(v)
			if label == "" {
				continue
			}
			out = append(out, map[string]string{
				"id":    fmt.Sprintf("opt_%d", i+1),
				"label": label,
			})
		case map[string]any:
			label := strings.TrimSpace(fmt.Sprint(v["label"]))
			if label == "" || label == "<nil>" {
				label = strings.TrimSpace(fmt.Sprint(v["text"]))
			}
			if label == "" || label == "<nil>" {
				continue
			}
			id := strings.TrimSpace(fmt.Sprint(v["id"]))
			if id == "" || id == "<nil>" {
				id = fmt.Sprintf("opt_%d", i+1)
			}
			out = append(out, map[string]string{"id": id, "label": label})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// extractOptionsFromText pulls simple list choices from a clarifying question.
func extractOptionsFromText(content string) []map[string]string {
	lines := strings.Split(content, "\n")
	var out []map[string]string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		// "- label", "* label", "1. label", "1) label"
		label := ""
		switch {
		case strings.HasPrefix(s, "- "), strings.HasPrefix(s, "* "):
			label = strings.TrimSpace(s[2:])
		default:
			i := 0
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			if i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')') {
				label = strings.TrimSpace(s[i+1:])
			}
		}
		if label == "" || strings.HasSuffix(label, "?") && len(label) > 80 {
			continue
		}
		if len(label) > 80 {
			continue
		}
		out = append(out, map[string]string{
			"id":    fmt.Sprintf("opt_%d", len(out)+1),
			"label": label,
		})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func looksLikeClarifyingQuestion(content string) bool {
	s := strings.TrimSpace(content)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	// Short confirmatory answers are not asks.
	if len(s) < 12 {
		return false
	}
	cues := []string{
		"could you",
		"can you",
		"would you",
		"please clarify",
		"please confirm",
		"please provide",
		"please tell",
		"i need to know",
		"i need more",
		"which one",
		"what date",
		"what time",
		"which airport",
		"do you want",
		"do you prefer",
		"let me know",
		"reply with",
		"need your",
		"before i continue",
		"before i proceed",
		"to proceed",
		"to continue",
	}
	for _, c := range cues {
		if strings.Contains(lower, c) {
			return true
		}
	}
	// Ends with a question and mentions needing input-ish shape.
	if strings.HasSuffix(s, "?") && (strings.Contains(lower, "you") || strings.Contains(lower, "which") || strings.Contains(lower, "what") || strings.Contains(lower, "when") || strings.Contains(lower, "where")) {
		return true
	}
	return false
}

func compressIfNeeded(messages []providers.ChatMessage, goal string) []providers.ChatMessage {
	chars := 0
	for _, m := range messages {
		chars += utf8.RuneCountInString(m.Content)
	}
	if chars < DefaultMaxTokens*4 {
		return messages
	}
	if len(messages) <= compressKeepHead+compressKeepTail+1 {
		return messages
	}
	head := messages[:compressKeepHead]
	tail := messages[len(messages)-compressKeepTail:]
	middle := messages[compressKeepHead : len(messages)-compressKeepTail]
	var b strings.Builder
	b.WriteString("Compressed earlier steps for goal: ")
	b.WriteString(goal)
	b.WriteString("\n")
	for _, m := range middle {
		role := m.Role
		snippet := truncate(m.Content, 240)
		if snippet == "" && len(m.ToolCalls) > 0 {
			snippet = fmt.Sprintf("tool_calls=%d", len(m.ToolCalls))
		}
		b.WriteString("- ")
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(snippet)
		b.WriteString("\n")
	}
	out := make([]providers.ChatMessage, 0, compressKeepHead+1+compressKeepTail)
	out = append(out, head...)
	out = append(out, providers.ChatMessage{Role: "system", Content: b.String()})
	out = append(out, tail...)
	return out
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func jsonRawOrString(args string) any {
	args = strings.TrimSpace(args)
	if args == "" {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err == nil {
		return v
	}
	return args
}
