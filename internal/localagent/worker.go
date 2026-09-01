package localagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Worker struct {
	API          *APIClient
	Store        *CloudStore
	SupportDir   string
	Workspaces   map[string]string // id -> path
	BrowserURL   string
	Paused       bool
	ActiveRunID  string
	cancelActive context.CancelFunc
	heartbeatOnce sync.Once
	mu            sync.Mutex
}

func (w *Worker) SetPaused(v bool) {
	w.mu.Lock()
	w.Paused = v
	w.mu.Unlock()
}

func (w *Worker) SetWorkspaces(ws map[string]string) {
	w.mu.Lock()
	w.Workspaces = ws
	w.mu.Unlock()
}

func (w *Worker) Snapshot() (paused bool, active string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Paused, w.ActiveRunID
}

func (w *Worker) workspacePath(id string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.Workspaces == nil {
		return ""
	}
	return w.Workspaces[id]
}

func (w *Worker) Loop(ctx context.Context) error {
	if err := w.drainOnce(ctx); err != nil {
		log.Warn("desktop drain failed", map[string]any{"error": err.Error()})
	}
	w.heartbeatOnce.Do(func() {
		go w.heartbeatLoop(ctx)
	})
	return w.socketLoop(ctx)
}

func (w *Worker) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = w.API.Do(ctx, http.MethodPost, "/desktop/devices/"+url.PathEscape(w.API.DeviceID)+"/heartbeat", map[string]any{}, nil)
		}
	}
}

func (w *Worker) socketLoop(ctx context.Context) error {
	u, err := url.Parse(w.API.BaseURL)
	if err != nil {
		return err
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/desktop/runner?device_id=%s", scheme, u.Host, url.QueryEscape(w.API.DeviceID))
	header := http.Header{}
	header.Set("Authorization", "Bearer "+w.API.token())
	header.Set("X-Donna-Device-Id", w.API.DeviceID)
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var ev struct {
			Kind    string         `json:"kind"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		switch ev.Kind {
		case "run.available":
			go w.drainOnce(context.Background())
		case "run.cancel":
			if id, _ := ev.Payload["run_id"].(string); id != "" {
				w.cancelRun(id)
			}
		case "run.redirect", "run.approval_resolved":
			// harness polls the store; nothing extra required
		case "device.revoked":
			return fmt.Errorf("device_revoked")
		}
	}
}

func (w *Worker) cancelRun(runID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ActiveRunID == runID && w.cancelActive != nil {
		w.cancelActive()
	}
}

func (w *Worker) DrainOnce(ctx context.Context) error {
	return w.drainOnce(ctx)
}

func (w *Worker) drainOnce(ctx context.Context) error {
	w.mu.Lock()
	paused := w.Paused
	busy := w.ActiveRunID != ""
	w.mu.Unlock()
	if paused || busy {
		return nil
	}
	var queued []storage.AgentRun
	if err := w.API.Do(ctx, http.MethodGet, "/agent-runs?status=queued&limit=20", nil, &queued); err != nil {
		return err
	}
	var running []storage.AgentRun
	_ = w.API.Do(ctx, http.MethodGet, "/agent-runs?status=running&limit=20", nil, &running)
	runs := append(queued, running...)
	for _, run := range runs {
		if run.ExecutionTarget != storage.ExecutionTargetLocal {
			continue
		}
		if run.AssignedDeviceID == nil || *run.AssignedDeviceID != w.API.DeviceID {
			continue
		}
		return w.runOne(ctx, run.ID)
	}
	return nil
}

func (w *Worker) runOne(ctx context.Context, runID string) error {
	var claimed storage.AgentRun
	if err := w.API.Do(ctx, http.MethodPost, "/desktop/runs/"+url.PathEscape(runID)+"/claim", map[string]any{
		"worker_id": "desktop:" + w.API.DeviceID,
	}, &claimed); err != nil {
		return err
	}
	wsPath := ""
	if claimed.WorkspaceID != nil {
		wsPath = w.workspacePath(*claimed.WorkspaceID)
		if wsPath == "" {
			_, _ = w.Store.Patch(ctx, claimed.UserID, claimed.ID, map[string]any{
				"status":         storage.AgentStatusQueued,
				"waiting_reason": storage.WaitingWorkspaceUnavailable,
			})
			return fmt.Errorf("workspace_unavailable")
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.ActiveRunID = claimed.ID
	w.cancelActive = cancel
	w.mu.Unlock()
	defer func() {
		cancel()
		w.mu.Lock()
		w.ActiveRunID = ""
		w.cancelActive = nil
		w.mu.Unlock()
	}()

	reg := DesktopRegistry(&HTTPMemory{API: w.API}, &HTTPNotes{API: w.API}, w.BrowserURL, wsPath)
	harness := &agents.Harness{
		Store:    w.Store,
		LLM:      &RemoteCompleter{API: w.API},
		Registry: reg,
		WorkerID: "desktop:" + w.API.DeviceID,
	}
	return harness.Run(WithRunID(runCtx, claimed.ID), claimed)
}
