package control

import (
	"testing"
	"time"
)

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	c := NewCircuitBreaker(2, 100*time.Millisecond)
	now := time.Now()

	if c.State() != CircuitClosed {
		t.Fatalf("expected closed, got %s", c.State())
	}

	c.RecordFailure("provider_api", now)
	if c.State() != CircuitClosed {
		t.Fatalf("expected closed after first failure, got %s", c.State())
	}

	c.RecordFailure("provider_api", now)
	if c.State() != CircuitOpen {
		t.Fatalf("expected open after threshold failures, got %s", c.State())
	}

	if c.Allow(now.Add(10 * time.Millisecond)) {
		t.Fatal("expected deny while cooldown not elapsed")
	}
	if !c.Allow(now.Add(120 * time.Millisecond)) {
		t.Fatal("expected allow after cooldown")
	}
	if c.State() != CircuitHalfOpen {
		t.Fatalf("expected half_open, got %s", c.State())
	}

	c.RecordSuccess()
	if c.State() != CircuitClosed {
		t.Fatalf("expected closed after probe success, got %s", c.State())
	}
}
