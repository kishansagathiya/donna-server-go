package jobs

import (
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestRetryDelay(t *testing.T) {
	base := time.Minute
	if got := RetryDelay(1, base); got != base {
		t.Fatalf("attempt 1: got %v want %v", got, base)
	}
	if got := RetryDelay(3, base); got != 4*base {
		t.Fatalf("attempt 3: got %v want %v", got, 4*base)
	}
}

func TestShouldDeadLetter(t *testing.T) {
	if ShouldDeadLetter(4, 5) {
		t.Fatal("4 attempts should not dead-letter when max is 5")
	}
	if !ShouldDeadLetter(5, 5) {
		t.Fatal("5 attempts should dead-letter when max is 5")
	}
}

func TestJobTimeout(t *testing.T) {
	if got := jobTimeout(storage.JobTypeNoteEnrich); got != defaultJobTimeout {
		t.Fatalf("note enrich: got %v want %v", got, defaultJobTimeout)
	}
	if got := jobTimeout(storage.JobTypeAgentRun); got != agentJobTimeout {
		t.Fatalf("agent run: got %v want %v", got, agentJobTimeout)
	}
	if agentJobTimeout <= defaultJobTimeout {
		t.Fatal("agent jobs must outlive the default 2m worker tick")
	}
}
