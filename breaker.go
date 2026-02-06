package supercache

import (
	"sync/atomic"
	"time"
)

type circuitState int32

const (
	stateClosed   circuitState = 0
	stateOpen     circuitState = 1
	stateHalfOpen circuitState = 2
)

// CircuitBreaker implements a lightweight circuit breaker pattern.
// It transitions between Closed → Open → Half-Open states based on failure counts.
type CircuitBreaker struct {
	state       atomic.Int32
	failures    atomic.Int64
	lastFailure atomic.Int64 // unix nano timestamp
	threshold   int64
	cooldown    time.Duration
}

// NewCircuitBreaker creates a new CircuitBreaker with the given config.
func NewCircuitBreaker(threshold int64, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		cooldown:  cooldown,
	}
}

// Allow returns true if a request should be allowed through.
// Closed → always allow
// Open → allow if cooldown expired (transition to Half-Open)
// Half-Open → deny (one request already in flight)
func (cb *CircuitBreaker) Allow() bool {
	state := circuitState(cb.state.Load())
	switch state {
	case stateClosed:
		return true
	case stateOpen:
		lastFail := time.Unix(0, cb.lastFailure.Load())
		if time.Since(lastFail) >= cb.cooldown {
			// Try to transition to Half-Open (only one goroutine succeeds)
			if cb.state.CompareAndSwap(int32(stateOpen), int32(stateHalfOpen)) {
				return true
			}
		}
		return false
	case stateHalfOpen:
		// Only one request is allowed in Half-Open; deny the rest
		return false
	default:
		return true
	}
}

// RecordSuccess records a successful operation and resets the breaker to Closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.failures.Store(0)
	cb.state.Store(int32(stateClosed))
}

// RecordFailure records a failed operation. If failures reach the threshold,
// the breaker transitions to Open.
func (cb *CircuitBreaker) RecordFailure() {
	cb.lastFailure.Store(time.Now().UnixNano())
	failures := cb.failures.Add(1)
	if failures >= cb.threshold {
		cb.state.Store(int32(stateOpen))
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() circuitState {
	return circuitState(cb.state.Load())
}
