package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

type Action struct {
	ID          string          `json:"id"`
	Slug        string          `json:"slug"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Runner      string          `json:"runner"`
	Risk        string          `json:"risk"`
	InputSchema json.RawMessage `json:"input_schema"`
	Config      json.RawMessage `json:"config"`
	OwnerType   string          `json:"owner_type"`
	OwnerUserID *string         `json:"owner_user_id,omitempty"`
	Enabled     bool            `json:"enabled"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type Intent struct {
	ID              string          `json:"id"`
	UserID          string          `json:"user_id"`
	Kind            string          `json:"kind"`
	Status          string          `json:"status"`
	Summary         string          `json:"summary"`
	Slots           json.RawMessage `json:"slots"`
	SourceType      string          `json:"source_type"`
	SourceID        *string         `json:"source_id,omitempty"`
	SourceTurnIndex *int            `json:"source_turn_index,omitempty"`
	Confidence      *float64        `json:"confidence,omitempty"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

type ActionRun struct {
	ID          string          `json:"id"`
	UserID      string          `json:"user_id"`
	IntentID    *string         `json:"intent_id,omitempty"`
	ActionID    string          `json:"action_id"`
	Status      string          `json:"status"`
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output,omitempty"`
	Error       *string         `json:"error,omitempty"`
	ConfirmedAt *string         `json:"confirmed_at,omitempty"`
	StartedAt   *string         `json:"started_at,omitempty"`
	FinishedAt  *string         `json:"finished_at,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	// Optional joined fields for API responses.
	ActionSlug   *string `json:"action_slug,omitempty"`
	ActionName   *string `json:"action_name,omitempty"`
	ActionRisk   *string `json:"action_risk,omitempty"`
	AgentRunID   *string `json:"agent_run_id,omitempty"`
	ApprovalKind *string `json:"approval_kind,omitempty"`
}

type NewIntentInput struct {
	Kind            string
	Summary         string
	Slots           json.RawMessage
	SourceType      string
	SourceID        *string
	SourceTurnIndex *int
	Confidence      *float64
}

type NewActionRunInput struct {
	IntentID     *string
	ActionID     string
	Status       string
	Input        json.RawMessage
	AgentRunID   *string
	ApprovalKind string
}

// ActionsStore persists intents, actions, and action runs.
type ActionsStore struct {
	DB      *Supabase
	Enabled bool
}

func (a *ActionsStore) selectActionColumns() string {
	return "id,slug,name,description,runner,risk,input_schema,config,owner_type,owner_user_id,enabled,created_at,updated_at"
}

func (a *ActionsStore) selectIntentColumns() string {
	return "id,user_id,kind,status,summary,slots,source_type,source_id,source_turn_index,confidence,created_at,updated_at"
}

// Inbox list omits bulky slots; detail paths still use selectIntentColumns.
func (a *ActionsStore) selectIntentInboxColumns() string {
	return "id,user_id,kind,status,summary,source_type,source_id,source_turn_index,confidence,created_at,updated_at"
}

func (a *ActionsStore) selectRunColumns() string {
	return "id,user_id,intent_id,action_id,status,input,output,error,confirmed_at,started_at,finished_at,created_at,updated_at,agent_run_id,approval_kind"
}

// Inbox run projection skips input/output jsonb blobs.
func (a *ActionsStore) selectRunInboxColumns() string {
	return "id,user_id,intent_id,action_id,status,error,confirmed_at,started_at,finished_at,created_at,updated_at,agent_run_id,approval_kind"
}

func (a *ActionsStore) selectActionInboxColumns() string {
	return "id,slug,name,risk"
}

// ---------------------------------------------------------------------------
// Actions registry
// ---------------------------------------------------------------------------

func (a *ActionsStore) ListEnabledActions(ctx context.Context, userID string) ([]Action, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return nil, fmt.Errorf("actions_disabled")
	}

	q := url.Values{}
	q.Set("select", a.selectActionColumns())
	q.Set("enabled", "eq.true")
	q.Set("or", fmt.Sprintf("(owner_type.eq.system,owner_user_id.eq.%s)", userID))
	q.Set("order", "owner_type.asc,slug.asc")

	var rows []Action
	if err := a.DB.Get(ctx, "actions", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Action{}
	}
	return rows, nil
}

func (a *ActionsStore) GetActionByID(ctx context.Context, actionID string) (Action, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return Action{}, fmt.Errorf("actions_disabled")
	}
	q := url.Values{}
	q.Set("select", a.selectActionColumns())
	q.Set("id", "eq."+actionID)
	q.Set("limit", "1")

	var rows []Action
	if err := a.DB.Get(ctx, "actions", q, &rows); err != nil {
		return Action{}, err
	}
	if len(rows) == 0 {
		return Action{}, fmt.Errorf("action_not_found")
	}
	return rows[0], nil
}

func (a *ActionsStore) GetSystemActionBySlug(ctx context.Context, slug string) (Action, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return Action{}, fmt.Errorf("actions_disabled")
	}
	q := url.Values{}
	q.Set("select", a.selectActionColumns())
	q.Set("slug", "eq."+slug)
	q.Set("owner_type", "eq.system")
	q.Set("enabled", "eq.true")
	q.Set("limit", "1")

	var rows []Action
	if err := a.DB.Get(ctx, "actions", q, &rows); err != nil {
		return Action{}, err
	}
	if len(rows) == 0 {
		return Action{}, fmt.Errorf("action_not_found")
	}
	return rows[0], nil
}

// ---------------------------------------------------------------------------
// Intents
// ---------------------------------------------------------------------------

func (a *ActionsStore) CreateIntent(ctx context.Context, userID string, in NewIntentInput) (Intent, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return Intent{}, fmt.Errorf("actions_disabled")
	}
	if strings.TrimSpace(in.Kind) == "" || strings.TrimSpace(in.Summary) == "" {
		return Intent{}, fmt.Errorf("kind_and_summary_required")
	}
	slots := in.Slots
	if len(slots) == 0 {
		slots = json.RawMessage(`{}`)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{
		"user_id":     userID,
		"kind":        strings.TrimSpace(in.Kind),
		"status":      "open",
		"summary":     strings.TrimSpace(in.Summary),
		"slots":       slots,
		"source_type": in.SourceType,
		"created_at":  now,
		"updated_at":  now,
	}
	if in.SourceID != nil {
		body["source_id"] = *in.SourceID
	}
	if in.SourceTurnIndex != nil {
		body["source_turn_index"] = *in.SourceTurnIndex
	}
	if in.Confidence != nil {
		body["confidence"] = *in.Confidence
	}

	var rows []Intent
	if err := a.DB.Insert(ctx, "intents", body, &rows); err != nil {
		return Intent{}, err
	}
	if len(rows) == 0 {
		return Intent{}, fmt.Errorf("intent_insert_empty")
	}
	return rows[0], nil
}

// UpsertOpenIntent creates an open intent or returns the existing open one for
// the same user/source/kind (unique partial index).
func (a *ActionsStore) UpsertOpenIntent(ctx context.Context, userID string, in NewIntentInput) (Intent, bool, error) {
	existing, err := a.findOpenIntent(ctx, userID, in)
	if err == nil {
		return existing, false, nil
	}
	created, err := a.CreateIntent(ctx, userID, in)
	if err != nil {
		// Race: another writer may have inserted between find and create.
		if existing, findErr := a.findOpenIntent(ctx, userID, in); findErr == nil {
			return existing, false, nil
		}
		return Intent{}, false, err
	}
	return created, true, nil
}

func (a *ActionsStore) findOpenIntent(ctx context.Context, userID string, in NewIntentInput) (Intent, error) {
	q := url.Values{}
	q.Set("select", a.selectIntentColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("status", "eq.open")
	q.Set("kind", "eq."+strings.TrimSpace(in.Kind))
	q.Set("source_type", "eq."+in.SourceType)
	if in.SourceID != nil {
		q.Set("source_id", "eq."+*in.SourceID)
	} else {
		q.Set("source_id", "is.null")
	}
	if in.SourceTurnIndex != nil {
		q.Set("source_turn_index", fmt.Sprintf("eq.%d", *in.SourceTurnIndex))
	} else {
		q.Set("source_turn_index", "is.null")
	}
	q.Set("limit", "1")

	var rows []Intent
	if err := a.DB.Get(ctx, "intents", q, &rows); err != nil {
		return Intent{}, err
	}
	if len(rows) == 0 {
		return Intent{}, fmt.Errorf("intent_not_found")
	}
	return rows[0], nil
}

func (a *ActionsStore) ListIntents(ctx context.Context, userID, status string, limit, offset int) ([]Intent, error) {
	return a.listIntents(ctx, userID, status, limit, offset, a.selectIntentColumns())
}

// ListIntentsInbox returns intents with slim columns for the Actions inbox.
func (a *ActionsStore) ListIntentsInbox(ctx context.Context, userID, status string, limit, offset int) ([]Intent, error) {
	return a.listIntents(ctx, userID, status, limit, offset, a.selectIntentInboxColumns())
}

func (a *ActionsStore) listIntents(ctx context.Context, userID, status string, limit, offset int, columns string) ([]Intent, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return nil, fmt.Errorf("actions_disabled")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	q := url.Values{}
	q.Set("select", columns)
	q.Set("user_id", "eq."+userID)
	if status != "" {
		q.Set("status", "eq."+status)
	}
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))

	var rows []Intent
	if err := a.DB.Get(ctx, "intents", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Intent{}
	}
	for i := range rows {
		if len(rows[i].Slots) == 0 {
			rows[i].Slots = json.RawMessage(`{}`)
		}
	}
	return rows, nil
}

// ListActiveRunsForIntents fetches active runs for many intents in one query.
func (a *ActionsStore) ListActiveRunsForIntents(ctx context.Context, userID string, intentIDs []string) ([]ActionRun, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return nil, fmt.Errorf("actions_disabled")
	}
	if len(intentIDs) == 0 {
		return []ActionRun{}, nil
	}
	q := url.Values{}
	q.Set("select", a.selectRunInboxColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("intent_id", "in.("+strings.Join(intentIDs, ",")+")")
	q.Set("status", "in.(proposed,confirmed,running)")
	q.Set("order", "created_at.desc")

	var rows []ActionRun
	if err := a.DB.Get(ctx, "action_runs", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []ActionRun{}
	}
	for i := range rows {
		if len(rows[i].Input) == 0 {
			rows[i].Input = json.RawMessage(`{}`)
		}
	}
	return rows, nil
}

// GetActionsByIDs fetches action registry rows for enrichment in one query.
func (a *ActionsStore) GetActionsByIDs(ctx context.Context, actionIDs []string) (map[string]Action, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return nil, fmt.Errorf("actions_disabled")
	}
	out := make(map[string]Action, len(actionIDs))
	if len(actionIDs) == 0 {
		return out, nil
	}
	q := url.Values{}
	q.Set("select", a.selectActionInboxColumns())
	q.Set("id", "in.("+strings.Join(actionIDs, ",")+")")

	var rows []Action
	if err := a.DB.Get(ctx, "actions", q, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, nil
}

func (a *ActionsStore) GetIntent(ctx context.Context, userID, intentID string) (Intent, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return Intent{}, fmt.Errorf("actions_disabled")
	}
	q := url.Values{}
	q.Set("select", a.selectIntentColumns())
	q.Set("id", "eq."+intentID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")

	var rows []Intent
	if err := a.DB.Get(ctx, "intents", q, &rows); err != nil {
		return Intent{}, err
	}
	if len(rows) == 0 {
		return Intent{}, fmt.Errorf("intent_not_found")
	}
	return rows[0], nil
}

func (a *ActionsStore) UpdateIntentStatus(ctx context.Context, userID, intentID, status string) (Intent, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return Intent{}, fmt.Errorf("actions_disabled")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("id", "eq."+intentID)
	q.Set("user_id", "eq."+userID)

	if err := a.DB.Patch(ctx, "intents", q, map[string]any{
		"status":     status,
		"updated_at": now,
	}); err != nil {
		return Intent{}, err
	}
	return a.GetIntent(ctx, userID, intentID)
}

func (a *ActionsStore) DismissIntent(ctx context.Context, userID, intentID string) (Intent, error) {
	intent, err := a.GetIntent(ctx, userID, intentID)
	if err != nil {
		return Intent{}, err
	}
	if intent.Status != "open" {
		return intent, fmt.Errorf("intent_not_open")
	}
	updated, err := a.UpdateIntentStatus(ctx, userID, intentID, "dismissed")
	if err != nil {
		return Intent{}, err
	}
	// Cancel any proposed runs for this intent.
	_ = a.cancelProposedRunsForIntent(ctx, userID, intentID)
	return updated, nil
}

func (a *ActionsStore) MarkIntentActed(ctx context.Context, userID, intentID string) error {
	_, err := a.UpdateIntentStatus(ctx, userID, intentID, "acted")
	return err
}

// ---------------------------------------------------------------------------
// Action runs
// ---------------------------------------------------------------------------

func (a *ActionsStore) CreateActionRun(ctx context.Context, userID string, in NewActionRunInput) (ActionRun, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return ActionRun{}, fmt.Errorf("actions_disabled")
	}
	status := in.Status
	if status == "" {
		status = "proposed"
	}
	input := in.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{
		"user_id":    userID,
		"action_id":  in.ActionID,
		"status":     status,
		"input":      input,
		"created_at": now,
		"updated_at": now,
	}
	if in.IntentID != nil {
		body["intent_id"] = *in.IntentID
	}
	if in.AgentRunID != nil && *in.AgentRunID != "" {
		body["agent_run_id"] = *in.AgentRunID
	}
	if kind := strings.TrimSpace(in.ApprovalKind); kind != "" {
		body["approval_kind"] = kind
	}

	var rows []ActionRun
	if err := a.DB.Insert(ctx, "action_runs", body, &rows); err != nil {
		return ActionRun{}, err
	}
	if len(rows) == 0 {
		return ActionRun{}, fmt.Errorf("action_run_insert_empty")
	}
	return rows[0], nil
}

func (a *ActionsStore) GetActionRun(ctx context.Context, userID, runID string) (ActionRun, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return ActionRun{}, fmt.Errorf("actions_disabled")
	}
	q := url.Values{}
	q.Set("select", a.selectRunColumns())
	q.Set("id", "eq."+runID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")

	var rows []ActionRun
	if err := a.DB.Get(ctx, "action_runs", q, &rows); err != nil {
		return ActionRun{}, err
	}
	if len(rows) == 0 {
		return ActionRun{}, fmt.Errorf("action_run_not_found")
	}
	return rows[0], nil
}

func (a *ActionsStore) ListActionRuns(ctx context.Context, userID, status string, limit, offset int) ([]ActionRun, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return nil, fmt.Errorf("actions_disabled")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	q := url.Values{}
	q.Set("select", a.selectRunColumns())
	q.Set("user_id", "eq."+userID)
	if status != "" {
		q.Set("status", "eq."+status)
	}
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))

	var rows []ActionRun
	if err := a.DB.Get(ctx, "action_runs", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []ActionRun{}
	}
	return rows, nil
}

func (a *ActionsStore) FindActiveRunForIntent(ctx context.Context, userID, intentID string) (ActionRun, error) {
	q := url.Values{}
	q.Set("select", a.selectRunColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("intent_id", "eq."+intentID)
	q.Set("status", "in.(proposed,confirmed,running)")
	q.Set("limit", "1")

	var rows []ActionRun
	if err := a.DB.Get(ctx, "action_runs", q, &rows); err != nil {
		return ActionRun{}, err
	}
	if len(rows) == 0 {
		return ActionRun{}, fmt.Errorf("action_run_not_found")
	}
	return rows[0], nil
}

func (a *ActionsStore) UpdateActionRun(ctx context.Context, userID, runID string, patch map[string]any) (ActionRun, error) {
	if a == nil || !a.Enabled || a.DB == nil {
		return ActionRun{}, fmt.Errorf("actions_disabled")
	}
	if patch == nil {
		patch = map[string]any{}
	}
	patch["updated_at"] = time.Now().UTC().Format(time.RFC3339)

	q := url.Values{}
	q.Set("id", "eq."+runID)
	q.Set("user_id", "eq."+userID)

	if err := a.DB.Patch(ctx, "action_runs", q, patch); err != nil {
		return ActionRun{}, err
	}
	return a.GetActionRun(ctx, userID, runID)
}

func (a *ActionsStore) cancelProposedRunsForIntent(ctx context.Context, userID, intentID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("user_id", "eq."+userID)
	q.Set("intent_id", "eq."+intentID)
	q.Set("status", "eq.proposed")
	return a.DB.Patch(ctx, "action_runs", q, map[string]any{
		"status":     "cancelled",
		"updated_at": now,
	})
}

// SettleProposedForAgentRun marks proposed ledger rows for an agent run
// succeeded (Approved) or cancelled (Denied / steered away).
func (a *ActionsStore) SettleProposedForAgentRun(ctx context.Context, userID, agentRunID, status string) error {
	if a == nil || !a.Enabled || a.DB == nil {
		return fmt.Errorf("actions_disabled")
	}
	agentRunID = strings.TrimSpace(agentRunID)
	if agentRunID == "" {
		return nil
	}
	status = strings.TrimSpace(status)
	if status != "succeeded" && status != "cancelled" {
		return fmt.Errorf("invalid_settle_status")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("user_id", "eq."+userID)
	q.Set("agent_run_id", "eq."+agentRunID)
	q.Set("status", "eq.proposed")
	patch := map[string]any{
		"status":     status,
		"updated_at": now,
	}
	if status == "succeeded" {
		patch["confirmed_at"] = now
		patch["finished_at"] = now
	}
	return a.DB.Patch(ctx, "action_runs", q, patch)
}
