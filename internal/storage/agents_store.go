package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	AgentStatusQueued          = "queued"
	AgentStatusRunning         = "running"
	AgentStatusWaitingForUser  = "waiting_for_user"
	AgentStatusSucceeded       = "succeeded"
	AgentStatusFailed          = "failed"
	AgentStatusCancelled       = "cancelled"
	AgentStatusExpired         = "expired"

	AgentStepThought          = "thought"
	AgentStepToolCall         = "tool_call"
	AgentStepToolResult       = "tool_result"
	AgentStepMemoryRetrieve   = "memory_retrieve"
	AgentStepApprovalRequest  = "approval_request"
	AgentStepUserMessage      = "user_message"
	AgentStepStatus           = "status"
	AgentStepCompress         = "compress"
	AgentStepError            = "error"

	JobTypeAgentRun    = "agent_run"
	TargetKindAgentRun = "agent_run"
)

type AgentRun struct {
	ID               string          `json:"id"`
	UserID           string          `json:"user_id"`
	IntentID         *string         `json:"intent_id,omitempty"`
	Goal             string          `json:"goal"`
	Status           string          `json:"status"`
	Plan             json.RawMessage `json:"plan"`
	MemorySnapshot   json.RawMessage `json:"memory_snapshot"`
	ToolAllowlist    []string        `json:"tool_allowlist"`
	MaxSteps         int             `json:"max_steps"`
	StepCount        int             `json:"step_count"`
	RedirectPending  *string         `json:"redirect_pending,omitempty"`
	LeaseOwner       *string         `json:"lease_owner,omitempty"`
	LeaseUntil       *string         `json:"lease_until,omitempty"`
	LastHeartbeatAt  *string         `json:"last_heartbeat_at,omitempty"`
	Error            *string         `json:"error,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
	FinishedAt       *string         `json:"finished_at,omitempty"`
}

type AgentStep struct {
	ID         string          `json:"id"`
	AgentRunID string          `json:"agent_run_id"`
	UserID     string          `json:"user_id"`
	Seq        int             `json:"seq"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  string          `json:"created_at"`
}

type NewAgentRunInput struct {
	IntentID      *string
	Goal          string
	ToolAllowlist []string
	MaxSteps      int
	MemorySnapshot json.RawMessage
}

type AgentsStore struct {
	DB      *Supabase
	Enabled bool
}

func (s *AgentsStore) selectRunColumns() string {
	return "id,user_id,intent_id,goal,status,plan,memory_snapshot,tool_allowlist,max_steps,step_count,redirect_pending,lease_owner,lease_until,last_heartbeat_at,error,result,created_at,updated_at,finished_at"
}

func (s *AgentsStore) selectStepColumns() string {
	return "id,agent_run_id,user_id,seq,kind,payload,created_at"
}

func (s *AgentsStore) Create(ctx context.Context, userID string, in NewAgentRunInput) (AgentRun, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AgentRun{}, fmt.Errorf("agents_disabled")
	}
	goal := strings.TrimSpace(in.Goal)
	if goal == "" {
		return AgentRun{}, fmt.Errorf("goal_required")
	}
	maxSteps := in.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 80
	}
	allow := in.ToolAllowlist
	if allow == nil {
		allow = []string{}
	}
	mem := in.MemorySnapshot
	if len(mem) == 0 {
		mem = json.RawMessage(`{}`)
	}
	body := map[string]any{
		"user_id":         userID,
		"goal":            goal,
		"status":          AgentStatusQueued,
		"plan":            []any{},
		"memory_snapshot": mem,
		"tool_allowlist":  allow,
		"max_steps":       maxSteps,
		"step_count":      0,
	}
	if in.IntentID != nil && *in.IntentID != "" {
		body["intent_id"] = *in.IntentID
	}

	var rows []AgentRun
	if err := s.DB.Insert(ctx, "agent_runs", body, &rows); err != nil {
		return AgentRun{}, err
	}
	if len(rows) == 0 {
		return AgentRun{}, fmt.Errorf("agent_run_create_empty")
	}
	return rows[0], nil
}

func (s *AgentsStore) Get(ctx context.Context, userID, runID string) (AgentRun, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AgentRun{}, fmt.Errorf("agents_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectRunColumns())
	q.Set("id", "eq."+runID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []AgentRun
	if err := s.DB.Get(ctx, "agent_runs", q, &rows); err != nil {
		return AgentRun{}, err
	}
	if len(rows) == 0 {
		return AgentRun{}, fmt.Errorf("agent_run_not_found")
	}
	return rows[0], nil
}

func (s *AgentsStore) GetByID(ctx context.Context, runID string) (AgentRun, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AgentRun{}, fmt.Errorf("agents_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectRunColumns())
	q.Set("id", "eq."+runID)
	q.Set("limit", "1")
	var rows []AgentRun
	if err := s.DB.Get(ctx, "agent_runs", q, &rows); err != nil {
		return AgentRun{}, err
	}
	if len(rows) == 0 {
		return AgentRun{}, fmt.Errorf("agent_run_not_found")
	}
	return rows[0], nil
}

func (s *AgentsStore) List(ctx context.Context, userID, status string, limit, offset int) ([]AgentRun, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("agents_disabled")
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
	q.Set("select", s.selectRunColumns())
	q.Set("user_id", "eq."+userID)
	if status = strings.TrimSpace(status); status != "" {
		q.Set("status", "eq."+status)
	}
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))
	var rows []AgentRun
	if err := s.DB.Get(ctx, "agent_runs", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []AgentRun{}
	}
	return rows, nil
}

func (s *AgentsStore) Patch(ctx context.Context, userID, runID string, patch map[string]any) (AgentRun, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AgentRun{}, fmt.Errorf("agents_disabled")
	}
	if patch == nil {
		patch = map[string]any{}
	}
	patch["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	q := url.Values{}
	q.Set("id", "eq."+runID)
	q.Set("user_id", "eq."+userID)
	var rows []AgentRun
	if err := s.DB.PatchReturning(ctx, "agent_runs", q, patch, &rows); err != nil {
		return AgentRun{}, err
	}
	if len(rows) == 0 {
		return AgentRun{}, fmt.Errorf("agent_run_not_found")
	}
	return rows[0], nil
}

func (s *AgentsStore) Heartbeat(ctx context.Context, userID, runID, workerID string, lease time.Duration) (AgentRun, error) {
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	now := time.Now().UTC()
	until := now.Add(lease).Format(time.RFC3339Nano)
	return s.Patch(ctx, userID, runID, map[string]any{
		"lease_owner":        workerID,
		"lease_until":        until,
		"last_heartbeat_at":  now.Format(time.RFC3339Nano),
		"status":             AgentStatusRunning,
	})
}

func (s *AgentsStore) AppendStep(ctx context.Context, userID, runID string, seq int, kind string, payload map[string]any) (AgentStep, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AgentStep{}, fmt.Errorf("agents_disabled")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return AgentStep{}, err
	}
	body := map[string]any{
		"agent_run_id": runID,
		"user_id":      userID,
		"seq":          seq,
		"kind":         kind,
		"payload":      json.RawMessage(raw),
	}
	var rows []AgentStep
	if err := s.DB.Insert(ctx, "agent_steps", body, &rows); err != nil {
		return AgentStep{}, err
	}
	if len(rows) == 0 {
		return AgentStep{}, fmt.Errorf("agent_step_create_empty")
	}
	_, _ = s.Patch(ctx, userID, runID, map[string]any{"step_count": seq})
	return rows[0], nil
}

func (s *AgentsStore) ListSteps(ctx context.Context, userID, runID string, afterSeq, limit int) ([]AgentStep, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("agents_disabled")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	q := url.Values{}
	q.Set("select", s.selectStepColumns())
	q.Set("agent_run_id", "eq."+runID)
	q.Set("user_id", "eq."+userID)
	if afterSeq > 0 {
		q.Set("seq", "gt."+fmt.Sprintf("%d", afterSeq))
	}
	q.Set("order", "seq.asc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	var rows []AgentStep
	if err := s.DB.Get(ctx, "agent_steps", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []AgentStep{}
	}
	return rows, nil
}

func (s *AgentsStore) SetRedirect(ctx context.Context, userID, runID, message string) (AgentRun, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return AgentRun{}, fmt.Errorf("redirect_message_required")
	}
	run, err := s.Get(ctx, userID, runID)
	if err != nil {
		return AgentRun{}, err
	}
	switch run.Status {
	case AgentStatusQueued, AgentStatusRunning, AgentStatusWaitingForUser:
		// ok
	default:
		return AgentRun{}, fmt.Errorf("run_not_redirectable")
	}
	return s.Patch(ctx, userID, runID, map[string]any{
		"redirect_pending": msg,
	})
}

func (s *AgentsStore) Cancel(ctx context.Context, userID, runID string) (AgentRun, error) {
	run, err := s.Get(ctx, userID, runID)
	if err != nil {
		return AgentRun{}, err
	}
	switch run.Status {
	case AgentStatusSucceeded, AgentStatusFailed, AgentStatusCancelled, AgentStatusExpired:
		return run, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return s.Patch(ctx, userID, runID, map[string]any{
		"status":      AgentStatusCancelled,
		"finished_at": now,
		"redirect_pending": nil,
	})
}

func (s *AgentsStore) Finish(ctx context.Context, userID, runID, status string, result map[string]any, errText string) (AgentRun, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	patch := map[string]any{
		"status":           status,
		"finished_at":      now,
		"lease_owner":      nil,
		"lease_until":      nil,
		"redirect_pending": nil,
	}
	if result != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return AgentRun{}, err
		}
		patch["result"] = json.RawMessage(raw)
	}
	if errText != "" {
		patch["error"] = errText
	} else {
		patch["error"] = nil
	}
	return s.Patch(ctx, userID, runID, patch)
}

func (s *AgentsStore) WaitForUser(ctx context.Context, userID, runID string, approvalPayload map[string]any) (AgentRun, error) {
	return s.Patch(ctx, userID, runID, map[string]any{
		"status":      AgentStatusWaitingForUser,
		"lease_owner": nil,
		"lease_until": nil,
		"result":      approvalPayload,
	})
}

func (s *AgentsStore) Requeue(ctx context.Context, userID, runID string) (AgentRun, error) {
	return s.Patch(ctx, userID, runID, map[string]any{
		"status":      AgentStatusQueued,
		"lease_owner": nil,
		"lease_until": nil,
		"error":       nil,
		"finished_at": nil,
	})
}
