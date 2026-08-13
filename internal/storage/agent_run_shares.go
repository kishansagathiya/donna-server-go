package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AgentRunShare is an owned share link row (auth'd APIs).
type AgentRunShare struct {
	ID         string  `json:"id"`
	AgentRunID string  `json:"agent_run_id"`
	Token      string  `json:"token"`
	CreatedAt  string  `json:"created_at"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
}

// PublicSharedAgentTurnOutput is prompt-adjacent output only — never steps or memory.
type PublicSharedAgentTurnOutput struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

// PublicSharedAgentTurn is a safe turn payload for unauthenticated viewers.
type PublicSharedAgentTurn struct {
	Prompt string                      `json:"prompt"`
	Output PublicSharedAgentTurnOutput `json:"output"`
}

// PublicSharedAgentRun is returned by GET /share/agent/{token}.
type PublicSharedAgentRun struct {
	Goal      string                  `json:"goal"`
	Status    string                  `json:"status"`
	CreatedAt string                  `json:"created_at"`
	Turns     []PublicSharedAgentTurn `json:"turns"`
}

type agentShareRow struct {
	ID         string  `json:"id"`
	AgentRunID string  `json:"agent_run_id"`
	UserID     string  `json:"user_id"`
	Token      string  `json:"token"`
	CreatedAt  string  `json:"created_at"`
	RevokedAt  *string `json:"revoked_at"`
	ExpiresAt  *string `json:"expires_at"`
}

func (s *AgentsStore) getActiveShare(ctx context.Context, userID, runID string) (*AgentRunShare, error) {
	q := url.Values{}
	q.Set("select", "id,agent_run_id,token,created_at,revoked_at,expires_at")
	q.Set("agent_run_id", "eq."+runID)
	q.Set("user_id", "eq."+userID)
	q.Set("revoked_at", "is.null")
	q.Set("limit", "1")

	var rows []agentShareRow
	if err := s.DB.Get(ctx, "agent_run_shares", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	row := rows[0]
	if shareExpired(row.ExpiresAt) {
		return nil, nil
	}
	return &AgentRunShare{
		ID:         row.ID,
		AgentRunID: row.AgentRunID,
		Token:      row.Token,
		CreatedAt:  row.CreatedAt,
		RevokedAt:  row.RevokedAt,
		ExpiresAt:  row.ExpiresAt,
	}, nil
}

// GetShareForUser returns the active share for an owned agent run, if any.
func (s *AgentsStore) GetShareForUser(ctx context.Context, userID, runID string) (*AgentRunShare, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("agents_disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("missing agent run id")
	}
	if _, err := s.Get(ctx, userID, runID); err != nil {
		if strings.Contains(err.Error(), "not_found") {
			return nil, fmt.Errorf("agent run not found")
		}
		return nil, err
	}
	return s.getActiveShare(ctx, userID, runID)
}

// CreateShare ensures an active share token exists for the agent run.
// If one already exists, it is returned (idempotent).
func (s *AgentsStore) CreateShare(ctx context.Context, userID, runID string) (*AgentRunShare, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("agents_disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("missing agent run id")
	}
	if _, err := s.Get(ctx, userID, runID); err != nil {
		if strings.Contains(err.Error(), "not_found") {
			return nil, fmt.Errorf("agent run not found")
		}
		return nil, err
	}

	existing, err := s.getActiveShare(ctx, userID, runID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	token, err := newShareToken()
	if err != nil {
		return nil, err
	}

	var rows []agentShareRow
	body := map[string]any{
		"agent_run_id": runID,
		"user_id":      userID,
		"token":        token,
	}
	if err := s.DB.Insert(ctx, "agent_run_shares", body, &rows); err != nil {
		if existing, getErr := s.getActiveShare(ctx, userID, runID); getErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("failed to create share")
	}
	row := rows[0]
	return &AgentRunShare{
		ID:         row.ID,
		AgentRunID: row.AgentRunID,
		Token:      row.Token,
		CreatedAt:  row.CreatedAt,
		RevokedAt:  row.RevokedAt,
		ExpiresAt:  row.ExpiresAt,
	}, nil
}

// RevokeShare soft-revokes the active share for an owned agent run.
func (s *AgentsStore) RevokeShare(ctx context.Context, userID, runID string) error {
	if s == nil || !s.Enabled || s.DB == nil {
		return fmt.Errorf("agents_disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("missing agent run id")
	}
	if _, err := s.Get(ctx, userID, runID); err != nil {
		if strings.Contains(err.Error(), "not_found") {
			return fmt.Errorf("agent run not found")
		}
		return err
	}

	q := url.Values{}
	q.Set("agent_run_id", "eq."+runID)
	q.Set("user_id", "eq."+userID)
	q.Set("revoked_at", "is.null")
	body := map[string]any{
		"revoked_at": time.Now().UTC().Format(time.RFC3339),
	}
	return s.DB.Patch(ctx, "agent_run_shares", q, body)
}

func (s *AgentsStore) listAllSteps(ctx context.Context, userID, runID string) ([]AgentStep, error) {
	var all []AgentStep
	after := 0
	for {
		batch, err := s.ListSteps(ctx, userID, runID, after, 500)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 500 {
			break
		}
		after = batch[len(batch)-1].Seq
	}
	return all, nil
}

// GetPublicByShareToken resolves a public share token to a prompt+output view.
// Steps, memory_snapshot, and internal harness fields are never included.
func (s *AgentsStore) GetPublicByShareToken(ctx context.Context, token string) (*PublicSharedAgentRun, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("agents_disabled")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("missing token")
	}

	q := url.Values{}
	q.Set("select", "id,agent_run_id,user_id,token,created_at,revoked_at,expires_at")
	q.Set("token", "eq."+token)
	q.Set("revoked_at", "is.null")
	q.Set("limit", "1")

	var rows []agentShareRow
	if err := s.DB.Get(ctx, "agent_run_shares", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 || shareExpired(rows[0].ExpiresAt) {
		return nil, nil
	}
	share := rows[0]

	run, err := s.Get(ctx, share.UserID, share.AgentRunID)
	if err != nil {
		if strings.Contains(err.Error(), "not_found") {
			return nil, nil
		}
		return nil, err
	}

	steps, err := s.listAllSteps(ctx, share.UserID, share.AgentRunID)
	if err != nil {
		return nil, err
	}

	out := BuildPublicSharedAgentRun(run, steps)
	return &out, nil
}

func jsonObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func jsonString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func noneAgentOutput() PublicSharedAgentTurnOutput {
	return PublicSharedAgentTurnOutput{Kind: "none"}
}

func summaryAgentOutput(text string) PublicSharedAgentTurnOutput {
	return PublicSharedAgentTurnOutput{Kind: "summary", Text: text}
}

func questionAgentOutput(text string) PublicSharedAgentTurnOutput {
	return PublicSharedAgentTurnOutput{Kind: "question", Text: text}
}

func latestPublicOutput(run AgentRun) PublicSharedAgentTurnOutput {
	res := jsonObject(run.Result)
	if run.Status == AgentStatusWaitingForUser {
		q := jsonString(res, "question")
		if q == "" {
			q = jsonString(res, "summary")
		}
		if q != "" {
			return questionAgentOutput(q)
		}
		return noneAgentOutput()
	}
	if s := jsonString(res, "summary"); s != "" {
		return summaryAgentOutput(s)
	}
	if run.Error != nil {
		if e := strings.TrimSpace(*run.Error); e != "" {
			return summaryAgentOutput(e)
		}
	}
	return noneAgentOutput()
}

func priorTurnPublicOutput(steps []AgentStep) PublicSharedAgentTurnOutput {
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Kind != AgentStepApprovalRequest {
			continue
		}
		q := jsonString(jsonObject(steps[i].Payload), "question")
		if q != "" {
			return questionAgentOutput(q)
		}
	}
	return noneAgentOutput()
}

// BuildPublicSharedAgentRun maps a run to prompt + output turns.
// Thoughts, tool calls/results, and memory are used only to find user_message
// boundaries and prior ask_user questions — they are never copied into the DTO.
func BuildPublicSharedAgentRun(run AgentRun, steps []AgentStep) PublicSharedAgentRun {
	ordered := append([]AgentStep(nil), steps...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Seq < ordered[j].Seq })

	type bucket struct {
		prompt string
		steps  []AgentStep
	}
	buckets := []bucket{{prompt: run.Goal}}
	for _, step := range ordered {
		if step.Kind == AgentStepUserMessage {
			buckets = append(buckets, bucket{
				prompt: jsonString(jsonObject(step.Payload), "message"),
			})
			continue
		}
		last := &buckets[len(buckets)-1]
		last.steps = append(last.steps, step)
	}

	turns := make([]PublicSharedAgentTurn, 0, len(buckets))
	for i, b := range buckets {
		out := noneAgentOutput()
		if i == len(buckets)-1 {
			out = latestPublicOutput(run)
		} else {
			out = priorTurnPublicOutput(b.steps)
		}
		turns = append(turns, PublicSharedAgentTurn{
			Prompt: b.prompt,
			Output: out,
		})
	}

	return PublicSharedAgentRun{
		Goal:      run.Goal,
		Status:    run.Status,
		CreatedAt: run.CreatedAt,
		Turns:     turns,
	}
}
