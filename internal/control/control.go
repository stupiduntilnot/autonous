package control

import (
	"fmt"
	"time"
)

// Policy defines control-plane limits and retry behavior.
type Policy struct {
	MaxTurns    int
	MaxWallTime time.Duration
	MaxRetries  int
}

// DefaultPolicy returns the default milestone-3 policy.
func DefaultPolicy() Policy {
	return Policy{
		MaxTurns:    1,
		MaxWallTime: 120 * time.Second,
		MaxRetries:  3,
	}
}

// LimitType identifies which limit is reached.
type LimitType string

const (
	LimitTurns    LimitType = "max_turns"
	LimitWallTime LimitType = "max_wall_time_seconds"
)

// LimitError indicates a run limit was reached.
type LimitError struct {
	Type      LimitType
	Value     int64
	Threshold int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("limit reached type=%s value=%d threshold=%d", e.Type, e.Value, e.Threshold)
}

// CheckTurnLimit validates turn usage against policy.
func CheckTurnLimit(p Policy, usedTurns int) error {
	if p.MaxTurns <= 0 {
		return &LimitError{Type: LimitTurns, Value: int64(usedTurns), Threshold: int64(p.MaxTurns)}
	}
	if usedTurns >= p.MaxTurns {
		return &LimitError{Type: LimitTurns, Value: int64(usedTurns), Threshold: int64(p.MaxTurns)}
	}
	return nil
}

// CheckWallTime validates elapsed time against policy.
func CheckWallTime(p Policy, startedAt time.Time, now time.Time) error {
	limit := p.MaxWallTime
	if limit <= 0 {
		return &LimitError{Type: LimitWallTime, Value: 0, Threshold: int64(limit.Seconds())}
	}
	elapsed := now.Sub(startedAt)
	if elapsed > limit {
		return &LimitError{
			Type:      LimitWallTime,
			Value:     int64(elapsed.Seconds()),
			Threshold: int64(limit.Seconds()),
		}
	}
	return nil
}

// RetryBackoffSeconds computes exponential backoff with a fixed cap.
func RetryBackoffSeconds(attempt int) int {
	if attempt <= 0 {
		return 0
	}
	seconds := 1 << (attempt - 1)
	if seconds > 30 {
		return 30
	}
	return seconds
}

// ShouldRetry returns whether a failed attempt should be retried.
func ShouldRetry(p Policy, attempts int) bool {
	return attempts <= p.MaxRetries
}

// NoProgress returns whether the same fingerprint repeats k times.
func NoProgress(lastFingerprints []string, k int) bool {
	if k <= 1 || len(lastFingerprints) < k {
		return false
	}
	ref := lastFingerprints[len(lastFingerprints)-1]
	for i := len(lastFingerprints) - k; i < len(lastFingerprints)-1; i++ {
		if lastFingerprints[i] != ref {
			return false
		}
	}
	return true
}
