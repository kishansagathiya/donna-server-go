package jobs

import (
	"testing"
	"time"
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
