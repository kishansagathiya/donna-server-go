package agents

import (
	"context"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// RunStore is the persistence surface the harness needs (mockable in tests).
type RunStore interface {
	Get(ctx context.Context, userID, runID string) (storage.AgentRun, error)
	Patch(ctx context.Context, userID, runID string, patch map[string]any) (storage.AgentRun, error)
	Heartbeat(ctx context.Context, userID, runID, workerID string, lease time.Duration) (storage.AgentRun, error)
	AppendStep(ctx context.Context, userID, runID string, seq int, kind string, payload map[string]any) (storage.AgentStep, error)
	ListSteps(ctx context.Context, userID, runID string, afterSeq, limit int) ([]storage.AgentStep, error)
	Finish(ctx context.Context, userID, runID, status string, result map[string]any, errText string) (storage.AgentRun, error)
	WaitForUser(ctx context.Context, userID, runID string, approvalPayload map[string]any) (storage.AgentRun, error)
}

// Ensure *storage.AgentsStore satisfies RunStore.
var _ RunStore = (*storage.AgentsStore)(nil)
