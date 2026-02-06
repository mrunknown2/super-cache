package supercache

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCircuitBreaker_StartsInClosedState(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Second)
	assert.Equal(t, stateClosed, cb.State())
	assert.True(t, cb.Allow())
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Second)

	cb.RecordFailure()
	assert.Equal(t, stateClosed, cb.State())
	assert.True(t, cb.Allow())

	cb.RecordFailure()
	assert.Equal(t, stateClosed, cb.State())
	assert.True(t, cb.Allow())

	cb.RecordFailure()
	assert.Equal(t, stateOpen, cb.State())
	assert.False(t, cb.Allow())
}

func TestCircuitBreaker_TransitionsToHalfOpenAfterCooldown(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, stateOpen, cb.State())
	assert.False(t, cb.Allow())

	// Wait for cooldown
	time.Sleep(60 * time.Millisecond)

	// Should transition to Half-Open and allow one request
	assert.True(t, cb.Allow())
	assert.Equal(t, stateHalfOpen, cb.State())

	// Second request should be denied in Half-Open
	assert.False(t, cb.Allow())
}

func TestCircuitBreaker_HalfOpenToClosedOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()

	time.Sleep(60 * time.Millisecond)
	cb.Allow() // transition to Half-Open

	cb.RecordSuccess()
	assert.Equal(t, stateClosed, cb.State())
	assert.True(t, cb.Allow())
}

func TestCircuitBreaker_HalfOpenToOpenOnFailure(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()

	time.Sleep(60 * time.Millisecond)
	cb.Allow() // transition to Half-Open

	cb.RecordFailure()
	assert.Equal(t, stateOpen, cb.State())
	assert.False(t, cb.Allow())
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Second)

	cb.RecordFailure()
	cb.RecordFailure()
	// Two failures, then a success resets
	cb.RecordSuccess()
	assert.Equal(t, stateClosed, cb.State())

	// Now need 3 more failures to open
	cb.RecordFailure()
	assert.Equal(t, stateClosed, cb.State())
	cb.RecordFailure()
	assert.Equal(t, stateClosed, cb.State())
	cb.RecordFailure()
	assert.Equal(t, stateOpen, cb.State())
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(100, 50*time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.Allow()
			cb.RecordFailure()
			cb.RecordSuccess()
			cb.State()
		}()
	}
	wg.Wait()

	// Should not panic or race — just verify it completes
	state := cb.State()
	assert.Contains(t, []circuitState{stateClosed, stateOpen, stateHalfOpen}, state)
}
