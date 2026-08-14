package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	JobStatusPending    = "pending"
	JobStatusRunning    = "running"
	JobStatusSucceeded  = "succeeded"
	JobStatusDeadLetter = "dead_letter"

	JobTypeNoteEnrich          = "note_enrich"
	JobTypeNoteEmbed           = "note_embed"
	JobTypeNoteLinkExpand      = "note_link_expand"
	JobTypeMemoryExtract       = "memory_extract"
	JobTypeSmartTagEnrich      = "smart_tag_enrich"
	JobTypeChatGPTExportImport = "chatgpt_export_import"

	TargetKindNote         = "note"
	TargetKindFact         = "fact"
	TargetKindConversation = "conversation"
	TargetKindSource       = "source"
	TargetKindImport       = "import"
)

type BackgroundJob struct {
	ID            string          `json:"id"`
	UserID        *string         `json:"user_id"`
	JobType       string          `json:"job_type"`
	DedupeKey     *string         `json:"dedupe_key"`
	Payload       json.RawMessage `json:"payload"`
	TargetKind    *string         `json:"target_kind"`
	TargetID      *string         `json:"target_id"`
	TargetVersion *int64          `json:"target_version"`
	Status        string          `json:"status"`
	AttemptCount  int             `json:"attempt_count"`
	MaxAttempts   int             `json:"max_attempts"`
	RunAfter      string          `json:"run_after"`
	LastError     *string         `json:"last_error"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
	FinishedAt    *string         `json:"finished_at"`
}

type BackgroundJobs struct {
	DB      *Supabase
	Enabled bool
}

type EnqueueJobInput struct {
	UserID        string
	JobType       string
	DedupeKey     string
	Payload       map[string]any
	TargetKind    string
	TargetID      string
	TargetVersion int64
	RunAfter      time.Time
}

func (s *BackgroundJobs) Enqueue(ctx context.Context, in EnqueueJobInput) (BackgroundJob, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return BackgroundJob{}, fmt.Errorf("background jobs unavailable")
	}
	body := map[string]any{
		"p_user_id":    in.UserID,
		"p_job_type":   in.JobType,
		"p_dedupe_key": in.DedupeKey,
		"p_payload":    in.Payload,
	}
	if in.TargetKind != "" {
		body["p_target_kind"] = in.TargetKind
	}
	if in.TargetID != "" {
		body["p_target_id"] = in.TargetID
	}
	if in.TargetVersion > 0 {
		body["p_target_version"] = in.TargetVersion
	}
	if !in.RunAfter.IsZero() {
		body["p_run_after"] = in.RunAfter.UTC().Format(time.RFC3339Nano)
	}

	var rows []BackgroundJob
	if err := s.DB.RPC(ctx, "enqueue_background_job", body, &rows); err != nil {
		return BackgroundJob{}, err
	}
	if len(rows) == 0 {
		return BackgroundJob{}, fmt.Errorf("enqueue_background_job: empty result")
	}
	return rows[0], nil
}

func (s *BackgroundJobs) Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]BackgroundJob, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("background jobs unavailable")
	}
	if limit <= 0 {
		limit = 1
	}
	secs := int(lease.Seconds())
	if secs <= 0 {
		secs = 300
	}
	body := map[string]any{
		"p_worker_id":     workerID,
		"p_limit":         limit,
		"p_lease_seconds": secs,
	}
	var rows []BackgroundJob
	if err := s.DB.RPC(ctx, "claim_background_jobs", body, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *BackgroundJobs) Complete(ctx context.Context, jobID, workerID string) (BackgroundJob, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return BackgroundJob{}, fmt.Errorf("background jobs unavailable")
	}
	body := map[string]any{
		"p_job_id":    jobID,
		"p_worker_id": workerID,
	}
	var rows []BackgroundJob
	if err := s.DB.RPC(ctx, "complete_background_job", body, &rows); err != nil {
		return BackgroundJob{}, err
	}
	if len(rows) == 0 {
		return BackgroundJob{}, fmt.Errorf("complete_background_job: empty result")
	}
	return rows[0], nil
}

func (s *BackgroundJobs) Fail(ctx context.Context, jobID, workerID, errMsg string, retryDelay time.Duration) (BackgroundJob, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return BackgroundJob{}, fmt.Errorf("background jobs unavailable")
	}
	secs := int(retryDelay.Seconds())
	if secs <= 0 {
		secs = 60
	}
	body := map[string]any{
		"p_job_id":              jobID,
		"p_worker_id":           workerID,
		"p_error":               errMsg,
		"p_retry_delay_seconds": secs,
	}
	var rows []BackgroundJob
	if err := s.DB.RPC(ctx, "fail_background_job", body, &rows); err != nil {
		return BackgroundJob{}, err
	}
	if len(rows) == 0 {
		return BackgroundJob{}, fmt.Errorf("fail_background_job: empty result")
	}
	return rows[0], nil
}

func (s *BackgroundJobs) TargetIsStale(ctx context.Context, jobID string) (bool, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return false, fmt.Errorf("background jobs unavailable")
	}
	var stale bool
	if err := s.DB.RPC(ctx, "background_job_target_is_stale", map[string]any{
		"p_job_id": jobID,
	}, &stale); err != nil {
		return false, err
	}
	return stale, nil
}
