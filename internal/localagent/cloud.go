package localagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type APIClient struct {
	BaseURL  string
	Token    string
	DeviceID string
	HTTP     *http.Client
	mu       sync.RWMutex
}

func NewAPIClient(baseURL, token, deviceID string) *APIClient {
	return &APIClient{
		BaseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Token:    token,
		DeviceID: deviceID,
		HTTP:     &http.Client{Timeout: 2 * time.Minute},
	}
}

func (c *APIClient) SetToken(token string) {
	c.mu.Lock()
	c.Token = token
	c.mu.Unlock()
}

func (c *APIClient) token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Token
}

func (c *APIClient) Do(ctx context.Context, method, path string, body any, dest any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token())
	req.Header.Set("X-Donna-Device-Id", c.DeviceID)
	req.Header.Set("X-Donna-Client", "donna-desktop")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8_000_000))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		var parsed struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &parsed)
		if parsed.Error != "" {
			msg = parsed.Error
			if parsed.Message != "" {
				msg = parsed.Error + ": " + parsed.Message
			}
		}
		return fmt.Errorf("http_%d: %s", res.StatusCode, msg)
	}
	if dest != nil && len(raw) > 0 {
		return json.Unmarshal(raw, dest)
	}
	return nil
}

type CloudStore struct {
	API      *APIClient
	WorkerID string
}

var _ agents.RunStore = (*CloudStore)(nil)

func (s *CloudStore) Get(ctx context.Context, userID, runID string) (storage.AgentRun, error) {
	var run storage.AgentRun
	err := s.API.Do(ctx, http.MethodGet, "/agent-runs/"+url.PathEscape(runID), nil, &run)
	return run, err
}

func (s *CloudStore) Patch(ctx context.Context, userID, runID string, patch map[string]any) (storage.AgentRun, error) {
	var run storage.AgentRun
	err := s.API.Do(ctx, http.MethodPatch, "/desktop/runs/"+url.PathEscape(runID)+"/state", patch, &run)
	return run, err
}

func (s *CloudStore) Heartbeat(ctx context.Context, userID, runID, workerID string, lease time.Duration) (storage.AgentRun, error) {
	var run storage.AgentRun
	err := s.API.Do(ctx, http.MethodPost, "/desktop/runs/"+url.PathEscape(runID)+"/heartbeat", map[string]any{
		"worker_id": workerID,
	}, &run)
	return run, err
}

func (s *CloudStore) AppendStep(ctx context.Context, userID, runID string, seq int, kind string, payload map[string]any) (storage.AgentStep, error) {
	var steps []storage.AgentStep
	err := s.API.Do(ctx, http.MethodPost, "/desktop/runs/"+url.PathEscape(runID)+"/steps/batch", map[string]any{
		"steps": []map[string]any{{
			"seq":     seq,
			"kind":    kind,
			"payload": payload,
		}},
	}, &steps)
	if err != nil {
		return storage.AgentStep{}, err
	}
	if len(steps) == 0 {
		return storage.AgentStep{}, fmt.Errorf("step_empty")
	}
	return steps[0], nil
}

func (s *CloudStore) ListSteps(ctx context.Context, userID, runID string, afterSeq, limit int) ([]storage.AgentStep, error) {
	q := url.Values{}
	if afterSeq > 0 {
		q.Set("after_seq", fmt.Sprintf("%d", afterSeq))
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/agent-runs/" + url.PathEscape(runID) + "/steps"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var steps []storage.AgentStep
	err := s.API.Do(ctx, http.MethodGet, path, nil, &steps)
	return steps, err
}

func (s *CloudStore) Finish(ctx context.Context, userID, runID, status string, result map[string]any, errText string) (storage.AgentRun, error) {
	body := map[string]any{"status": status, "result": result, "error": errText}
	var run storage.AgentRun
	err := s.API.Do(ctx, http.MethodPatch, "/desktop/runs/"+url.PathEscape(runID)+"/state", body, &run)
	return run, err
}

func (s *CloudStore) WaitForUser(ctx context.Context, userID, runID string, approvalPayload map[string]any) (storage.AgentRun, error) {
	body := map[string]any{"status": storage.AgentStatusWaitingForUser, "result": approvalPayload}
	var run storage.AgentRun
	err := s.API.Do(ctx, http.MethodPatch, "/desktop/runs/"+url.PathEscape(runID)+"/state", body, &run)
	return run, err
}

type RemoteCompleter struct {
	API *APIClient
}

var _ agents.Completer = (*RemoteCompleter)(nil)

func (c *RemoteCompleter) CompleteOnceWithOptions(ctx context.Context, messages []providers.ChatMessage, options providers.ChatCompletionOptions) (providers.ChatCompletionMetadata, error) {
	runID := ""
	if v := ctx.Value(runIDContextKey{}); v != nil {
		runID, _ = v.(string)
	}
	var meta providers.ChatCompletionMetadata
	err := c.API.Do(ctx, http.MethodPost, "/desktop/model/complete", map[string]any{
		"run_id":      runID,
		"messages":    messages,
		"tools":       options.Tools,
		"tool_choice": options.ToolChoice,
	}, &meta)
	return meta, err
}

type runIDContextKey struct{}

func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDContextKey{}, runID)
}

type HTTPMemory struct {
	API *APIClient
}

func (m *HTTPMemory) Search(ctx context.Context, userID, query string, limit int) ([]agents.MemoryHit, error) {
	if limit <= 0 {
		limit = 6
	}
	var rows []struct {
		ID         string  `json:"id"`
		Content    string  `json:"content"`
		Fact       string  `json:"fact"`
		Text       string  `json:"text"`
		Confidence float64 `json:"confidence"`
	}
	path := fmt.Sprintf("/memory/facts?limit=%d", limit)
	if err := m.API.Do(ctx, http.MethodGet, path, nil, &rows); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]agents.MemoryHit, 0, len(rows))
	for _, row := range rows {
		text := firstNonEmpty(row.Text, row.Content, row.Fact)
		if q != "" && !strings.Contains(strings.ToLower(text), q) && len(out) > 0 {
			continue
		}
		out = append(out, agents.MemoryHit{Source: "fact", ID: row.ID, Text: text, Score: row.Confidence})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

type HTTPNotes struct {
	API *APIClient
}

func (n *HTTPNotes) Search(ctx context.Context, userID, query string, limit int) ([]agents.NoteHit, error) {
	if limit <= 0 {
		limit = 8
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", fmt.Sprintf("%d", limit))
	var rows []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Preview string `json:"preview"`
		Body    string `json:"body"`
	}
	if err := n.API.Do(ctx, http.MethodGet, "/notes/search?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}
	out := make([]agents.NoteHit, 0, len(rows))
	for _, row := range rows {
		preview := row.Preview
		if preview == "" {
			preview = row.Body
		}
		out = append(out, agents.NoteHit{ID: row.ID, Title: row.Title, Preview: preview})
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
