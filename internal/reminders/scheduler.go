package reminders

import (
	"context"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

// Scheduler marks due scheduled reminders as fired.
type Scheduler struct {
	Store    *Service
	Interval time.Duration
	Batch    int

	stop chan struct{}
}

func (s *Scheduler) Start() {
	if s == nil || s.Store == nil || s.Store.Store == nil || !s.Store.Store.Enabled {
		return
	}
	if s.Interval <= 0 {
		s.Interval = 5 * time.Second
	}
	if s.Batch <= 0 {
		s.Batch = 20
	}
	s.stop = make(chan struct{})
	go func() {
		t := time.NewTicker(s.Interval)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.tick()
			}
		}
	}()
	log.Print("reminders scheduler enabled", map[string]any{
		"intervalSec": int(s.Interval.Seconds()),
		"batch":       s.Batch,
	})
}

func (s *Scheduler) Stop() {
	if s != nil && s.stop != nil {
		close(s.stop)
	}
}

func (s *Scheduler) tick() {
	if s == nil || s.Store == nil || s.Store.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	due, err := s.Store.Store.ListDueScheduled(ctx, s.Batch)
	if err != nil {
		log.Warn("reminder scheduler list failed", map[string]any{"error": err.Error()})
		return
	}
	for _, rem := range due {
		if _, err := s.Store.Store.ClaimFired(ctx, rem); err != nil {
			if err.Error() == "reminder_claim_conflict" {
				continue
			}
			log.Warn("reminder fire failed", map[string]any{
				"reminderId": log.ShortID(rem.ID),
				"error":      err.Error(),
			})
		}
	}
}
