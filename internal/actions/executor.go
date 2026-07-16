package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Executor struct {
	Store   *storage.ActionsStore
	Builtin *BuiltinRunner
}

func (e *Executor) ConfirmAndExecute(ctx context.Context, userID, runID string) (storage.ActionRun, error) {
	if e == nil || e.Store == nil || !e.Store.Enabled {
		return storage.ActionRun{}, fmt.Errorf("actions_disabled")
	}
	run, err := e.Store.GetActionRun(ctx, userID, runID)
	if err != nil {
		return storage.ActionRun{}, err
	}
	switch run.Status {
	case "proposed", "confirmed":
		// proceed
	case "succeeded", "running":
		return run, nil
	default:
		return storage.ActionRun{}, fmt.Errorf("run_not_confirmable")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if run.Status == "proposed" {
		run, err = e.Store.UpdateActionRun(ctx, userID, runID, map[string]any{
			"status":       "confirmed",
			"confirmed_at": now,
		})
		if err != nil {
			return storage.ActionRun{}, err
		}
	}

	return e.execute(ctx, userID, run)
}

func (e *Executor) Cancel(ctx context.Context, userID, runID string) (storage.ActionRun, error) {
	if e == nil || e.Store == nil || !e.Store.Enabled {
		return storage.ActionRun{}, fmt.Errorf("actions_disabled")
	}
	run, err := e.Store.GetActionRun(ctx, userID, runID)
	if err != nil {
		return storage.ActionRun{}, err
	}
	if run.Status != "proposed" && run.Status != "confirmed" {
		return storage.ActionRun{}, fmt.Errorf("run_not_cancellable")
	}
	return e.Store.UpdateActionRun(ctx, userID, runID, map[string]any{
		"status": "cancelled",
	})
}

func (e *Executor) execute(ctx context.Context, userID string, run storage.ActionRun) (storage.ActionRun, error) {
	action, err := e.Store.GetActionByID(ctx, run.ActionID)
	if err != nil {
		return storage.ActionRun{}, err
	}
	if action.Runner != "builtin" {
		return storage.ActionRun{}, fmt.Errorf("runner_not_supported_phase1")
	}

	builtinName, err := BuiltinFromConfig(action.Config)
	if err != nil {
		return storage.ActionRun{}, err
	}
	if builtinName == "create_note" {
		return storage.ActionRun{}, fmt.Errorf("create_note_forbidden")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	run, err = e.Store.UpdateActionRun(ctx, userID, run.ID, map[string]any{
		"status":     "running",
		"started_at": now,
	})
	if err != nil {
		return storage.ActionRun{}, err
	}

	if e.Builtin == nil {
		e.Builtin = &BuiltinRunner{}
	}
	result, runErr := e.Builtin.Run(ctx, builtinName, InputMap(run.Input))
	finished := time.Now().UTC().Format(time.RFC3339)
	if runErr != nil {
		errText := runErr.Error()
		failed, patchErr := e.Store.UpdateActionRun(ctx, userID, run.ID, map[string]any{
			"status":      "failed",
			"error":       errText,
			"finished_at": finished,
		})
		if patchErr != nil {
			return storage.ActionRun{}, patchErr
		}
		return failed, runErr
	}

	outputJSON, err := json.Marshal(result.Output)
	if err != nil {
		return storage.ActionRun{}, err
	}
	succeeded, err := e.Store.UpdateActionRun(ctx, userID, run.ID, map[string]any{
		"status":      "succeeded",
		"output":      outputJSON,
		"finished_at": finished,
		"error":       nil,
	})
	if err != nil {
		return storage.ActionRun{}, err
	}

	if run.IntentID != nil && *run.IntentID != "" {
		_ = e.Store.MarkIntentActed(ctx, userID, *run.IntentID)
	}
	return succeeded, nil
}
