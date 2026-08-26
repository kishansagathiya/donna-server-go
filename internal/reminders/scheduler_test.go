package reminders

import (
	"context"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestSchedulerStartDisabled(t *testing.T) {
	s := &Scheduler{}
	s.Start()
	s.Stop()
	s.tick()

	s = &Scheduler{Store: &Service{Store: &storage.RemindersStore{Enabled: false}}}
	s.Start()
	s.tick()
}

func TestSchedulerTickFiresDue(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	store := testStore(t, []storage.Reminder{sampleReminder(past)})
	s := &Scheduler{Store: &Service{Store: store}, Batch: 5}
	s.tick()
	got, err := store.Get(context.Background(), "user-1", "rem-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != storage.ReminderStatusFired {
		t.Fatalf("status %s", got.Status)
	}

	s.tick() // claim conflict is ignored
}

func TestSchedulerStartAndStop(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	store := testStore(t, []storage.Reminder{sampleReminder(past)})
	s := &Scheduler{
		Store:    &Service{Store: store},
		Interval: 20 * time.Millisecond,
		Batch:    1,
	}
	s.Start()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, err := store.Get(context.Background(), "user-1", "rem-1")
		if err == nil && got.Status == storage.ReminderStatusFired {
			s.Stop()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop()
	t.Fatal("scheduler did not fire reminder")
}
