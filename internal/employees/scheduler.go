package employees

import (
	"context"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

// Scheduler wakes due active employees and starts shifts.
type Scheduler struct {
	Service  *Service
	Interval time.Duration
	Batch    int

	stop chan struct{}
}

func (s *Scheduler) Start() {
	if s == nil || s.Service == nil || s.Service.Store == nil || !s.Service.Store.Enabled {
		return
	}
	if s.Interval <= 0 {
		s.Interval = 15 * time.Second
	}
	if s.Batch <= 0 {
		s.Batch = 5
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
	log.Print("ai employees scheduler enabled", map[string]any{
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
	if s == nil || s.Service == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	due, err := s.Service.Store.ListDueActive(ctx, s.Batch)
	if err != nil {
		log.Warn("employee scheduler list failed", map[string]any{"error": err.Error()})
		return
	}
	for _, emp := range due {
		if err := s.Service.StartShift(ctx, emp); err != nil {
			if err.Error() == "employee_already_working" || err.Error() == "employee_claim_conflict" {
				continue
			}
			log.Warn("employee shift start failed", map[string]any{
				"employeeId": log.ShortID(emp.ID),
				"error":      err.Error(),
			})
		}
	}
}
