package control

import "time"

type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// CircuitBreaker is a minimal per-error-class breaker.
type CircuitBreaker struct {
	Threshold int
	Cooldown  time.Duration

	state       CircuitState
	failures    map[string]int
	openedAt    time.Time
	openedClass string
}

func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &CircuitBreaker{
		Threshold: threshold,
		Cooldown:  cooldown,
		state:     CircuitClosed,
		failures:  map[string]int{},
	}
}

func (c *CircuitBreaker) State() CircuitState {
	return c.state
}

// Allow returns whether new work is allowed at this instant.
func (c *CircuitBreaker) Allow(now time.Time) bool {
	if c.state != CircuitOpen {
		return true
	}
	if now.Sub(c.openedAt) >= c.Cooldown {
		c.state = CircuitHalfOpen
		return true
	}
	return false
}

// RecordSuccess updates state after a successful probe/operation.
func (c *CircuitBreaker) RecordSuccess() {
	c.state = CircuitClosed
	c.openedClass = ""
	c.failures = map[string]int{}
}

// RecordFailure updates state after an error in the given class.
func (c *CircuitBreaker) RecordFailure(errClass string, now time.Time) {
	if errClass == "" {
		errClass = "unknown"
	}
	if c.state == CircuitHalfOpen {
		c.state = CircuitOpen
		c.openedAt = now
		c.openedClass = errClass
		return
	}
	c.failures[errClass]++
	if c.failures[errClass] >= c.Threshold {
		c.state = CircuitOpen
		c.openedAt = now
		c.openedClass = errClass
	}
}

func (c *CircuitBreaker) OpenedClass() string {
	return c.openedClass
}
