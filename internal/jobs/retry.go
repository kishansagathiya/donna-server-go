package jobs

import "time"

const DefaultMaxAttempts = 5

// RetryDelay returns exponential backoff for attempt N (1-based after a failure).
func RetryDelay(attemptAfterFailure int, base time.Duration) time.Duration {
	if attemptAfterFailure < 1 {
		attemptAfterFailure = 1
	}
	if base <= 0 {
		base = time.Minute
	}
	shift := attemptAfterFailure - 1
	if shift > 6 {
		shift = 6
	}
	return base * time.Duration(1<<shift)
}

// ShouldDeadLetter reports whether another failure would exhaust max attempts.
func ShouldDeadLetter(attemptCountAfterFailure, maxAttempts int) bool {
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	return attemptCountAfterFailure >= maxAttempts
}
