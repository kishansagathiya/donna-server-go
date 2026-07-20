package jobs

import (
	"context"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Handler processes one claimed background job. Return nil on success.
type Handler func(ctx context.Context, job storage.BackgroundJob) error

type Worker struct {
	Store    *storage.BackgroundJobs
	WorkerID string
	Handlers map[string]Handler
	Interval time.Duration
	Lease    time.Duration
	Batch    int

	stop chan struct{}
}

func (w *Worker) Start() {
	if w == nil || w.Store == nil || !w.Store.Enabled {
		return
	}
	if w.WorkerID == "" {
		w.WorkerID = "donna-server"
	}
	if w.Interval <= 0 {
		w.Interval = 2 * time.Second
	}
	if w.Lease <= 0 {
		w.Lease = 5 * time.Minute
	}
	if w.Batch <= 0 {
		w.Batch = 5
	}
	if w.Handlers == nil {
		w.Handlers = map[string]Handler{}
	}
	w.stop = make(chan struct{})
	go func() {
		t := time.NewTicker(w.Interval)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				w.tick()
			}
		}
	}()
}

func (w *Worker) Stop() {
	if w != nil && w.stop != nil {
		close(w.stop)
	}
}

func (w *Worker) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	jobs, err := w.Store.Claim(ctx, w.WorkerID, w.Batch, w.Lease)
	if err != nil {
		log.Warn("background job claim failed", map[string]any{"error": err.Error()})
		return
	}
	for _, job := range jobs {
		w.runOne(ctx, job)
	}
}

func (w *Worker) runOne(ctx context.Context, job storage.BackgroundJob) {
	stale, err := w.Store.TargetIsStale(ctx, job.ID)
	if err != nil {
		w.fail(ctx, job, err)
		return
	}
	if stale {
		if _, err := w.Store.Complete(ctx, job.ID, w.WorkerID); err != nil {
			log.Warn("stale job complete failed", map[string]any{"jobId": log.ShortID(job.ID), "error": err.Error()})
		}
		return
	}

	handler := w.Handlers[job.JobType]
	if handler == nil {
		if _, err := w.Store.Complete(ctx, job.ID, w.WorkerID); err != nil {
			log.Warn("noop job complete failed", map[string]any{"jobType": job.JobType, "error": err.Error()})
		}
		return
	}

	if err := handler(ctx, job); err != nil {
		w.fail(ctx, job, err)
		return
	}
	if _, err := w.Store.Complete(ctx, job.ID, w.WorkerID); err != nil {
		log.Warn("job complete failed", map[string]any{"jobId": log.ShortID(job.ID), "error": err.Error()})
	}
}

func (w *Worker) fail(ctx context.Context, job storage.BackgroundJob, runErr error) {
	delay := RetryDelay(job.AttemptCount+1, time.Minute)
	if _, failErr := w.Store.Fail(ctx, job.ID, w.WorkerID, runErr.Error(), delay); failErr != nil {
		log.Warn("job fail failed", map[string]any{"jobId": log.ShortID(job.ID), "error": failErr.Error()})
	}
}
