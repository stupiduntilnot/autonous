package control

import (
	"testing"
	"time"
)

func TestCheckTurnLimit(t *testing.T) {
	p := Policy{MaxTurns: 1}
	if err := CheckTurnLimit(p, 0); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if err := CheckTurnLimit(p, 1); err == nil {
		t.Fatal("expected limit error")
	}
}

func TestCheckWallTime(t *testing.T) {
	p := Policy{MaxWallTime: 2 * time.Second}
	start := time.Unix(100, 0)
	if err := CheckWallTime(p, start, start.Add(1*time.Second)); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if err := CheckWallTime(p, start, start.Add(3*time.Second)); err == nil {
		t.Fatal("expected wall-time limit error")
	}
}

func TestCheckTokenLimit(t *testing.T) {
	p := Policy{MaxTokens: 10}
	if err := CheckTokenLimit(p, 10); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if err := CheckTokenLimit(p, 11); err == nil {
		t.Fatal("expected token limit error")
	}
}

func TestRetryBackoffSeconds(t *testing.T) {
	cases := []struct {
		attempt int
		want    int
	}{
		{1, 1},
		{2, 2},
		{3, 4},
		{6, 30},
	}
	for _, c := range cases {
		got := RetryBackoffSeconds(c.attempt)
		if got != c.want {
			t.Fatalf("attempt=%d got=%d want=%d", c.attempt, got, c.want)
		}
	}
}

func TestShouldRetry(t *testing.T) {
	p := Policy{MaxRetries: 3}
	if !ShouldRetry(p, 1) {
		t.Fatal("attempt 1 should retry")
	}
	if !ShouldRetry(p, 3) {
		t.Fatal("attempt 3 should retry")
	}
	if ShouldRetry(p, 4) {
		t.Fatal("attempt 4 should not retry")
	}
}

func TestNoProgress(t *testing.T) {
	if !NoProgress([]string{"a", "a", "a"}, 3) {
		t.Fatal("expected no-progress")
	}
	if NoProgress([]string{"a", "b", "b"}, 3) {
		t.Fatal("did not expect no-progress")
	}
}
