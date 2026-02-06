package supercache

import (
	"math/rand/v2"
	"time"
)

// ttlWithJitter adds random jitter to TTL to prevent cache avalanche.
// jitterPercent is the percentage of TTL to use as jitter range (e.g., 0.1 = ±10%).
func ttlWithJitter(ttl time.Duration, jitterPercent float64) time.Duration {
	if jitterPercent <= 0 || ttl <= 0 {
		return ttl
	}

	jitterRange := float64(ttl) * jitterPercent
	jitter := (rand.Float64()*2 - 1) * jitterRange // Random between -jitterRange and +jitterRange

	result := time.Duration(float64(ttl) + jitter)
	if result < time.Second {
		return time.Second // Minimum 1 second
	}
	return result
}
