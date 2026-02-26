package supercache

import (
	"fmt"
	"time"
)

// CircuitBreakerConfig configures the circuit breaker.
// When set (non-nil), the circuit breaker will trip after FailureThreshold
// consecutive Redis errors and stop calling Redis for the Cooldown duration.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures before opening the circuit.
	FailureThreshold int64
	// Cooldown is how long to wait before allowing a probe request in Half-Open state.
	Cooldown time.Duration
}

// Options configures the cache behavior.
type Options struct {
	// KeyPrefix is prepended to all cache keys.
	KeyPrefix string
	// DefaultTTL is the default time-to-live for cache entries.
	DefaultTTL time.Duration
	// NullTTL is the TTL for null/empty values (should be shorter to allow retry).
	NullTTL time.Duration
	// JitterPercent is the TTL jitter percentage (0.0-1.0) to prevent cache avalanche.
	JitterPercent float64
	// Serializer is used for value serialization. Default: MsgPackSerializer.
	Serializer Serializer
	// Hooks for observability. Default: NoopHooks.
	Hooks Hooks
	// LocalCacheSize is the number of items to cache locally using TinyLFU.
	// Set to 0 to disable local cache.
	LocalCacheSize int
	// LocalCacheTTL is the TTL for local cache entries.
	LocalCacheTTL time.Duration
	// FallbackOnError when true, if Redis encounters a connection error,
	// GetOrSet will call fn() directly and Get will return ErrNotFound
	// instead of propagating the Redis error. This provides graceful degradation.
	FallbackOnError bool
	// CircuitBreaker configures the circuit breaker. Nil means disabled.
	CircuitBreaker *CircuitBreakerConfig
	// ScanBatchSize is the number of keys to scan per SCAN iteration in Clear/ClearPattern.
	// Default: 100.
	ScanBatchSize int64
	// MaxKeyLength is the maximum allowed length for a cache key.
	// Default: 1024.
	MaxKeyLength int
	// MaxBatchSize is the maximum number of items allowed in batch operations (MGet/MSet/MDelete).
	// Default: 1000.
	MaxBatchSize int
	// MaxValueSize is the maximum allowed size in bytes for a serialized value during MGet unmarshal.
	// Default: 1048576 (1MB).
	MaxValueSize int
}

// DefaultOptions returns Options with sensible defaults.
func DefaultOptions() Options {
	return Options{
		KeyPrefix:      "sc:",
		DefaultTTL:     5 * time.Minute,
		NullTTL:        30 * time.Second,
		JitterPercent:  0.1, // 10% jitter
		Serializer:     MsgPackSerializer{},
		Hooks:          NoopHooks{},
		LocalCacheSize: 1000,
		LocalCacheTTL:  time.Minute,
		ScanBatchSize:  100,
		MaxKeyLength:   1024,
		MaxBatchSize:   1000,
		MaxValueSize:   1048576, // 1MB
	}
}

// Validate checks if the options are valid.
func (o Options) Validate() error {
	if o.DefaultTTL <= 0 {
		return fmt.Errorf("%w: DefaultTTL must be positive", ErrInvalidConfig)
	}
	if o.NullTTL < 0 {
		return fmt.Errorf("%w: NullTTL must not be negative", ErrInvalidConfig)
	}
	if o.JitterPercent < 0 || o.JitterPercent > 1 {
		return fmt.Errorf("%w: JitterPercent must be between 0 and 1", ErrInvalidConfig)
	}
	if o.Serializer == nil {
		return fmt.Errorf("%w: Serializer must not be nil", ErrInvalidConfig)
	}
	if o.LocalCacheSize > 0 && o.LocalCacheTTL <= 0 {
		return fmt.Errorf("%w: LocalCacheTTL must be positive when LocalCacheSize > 0", ErrInvalidConfig)
	}
	if o.ScanBatchSize <= 0 {
		return fmt.Errorf("%w: ScanBatchSize must be positive", ErrInvalidConfig)
	}
	if o.MaxKeyLength <= 0 {
		return fmt.Errorf("%w: MaxKeyLength must be positive", ErrInvalidConfig)
	}
	if o.MaxBatchSize <= 0 {
		return fmt.Errorf("%w: MaxBatchSize must be positive", ErrInvalidConfig)
	}
	if o.MaxValueSize <= 0 {
		return fmt.Errorf("%w: MaxValueSize must be positive", ErrInvalidConfig)
	}
	return nil
}

// Option is a function that modifies Options.
type Option func(*Options)

// WithKeyPrefix sets the key prefix.
func WithKeyPrefix(prefix string) Option {
	return func(o *Options) {
		o.KeyPrefix = prefix
	}
}

// WithDefaultTTL sets the default TTL.
func WithDefaultTTL(ttl time.Duration) Option {
	return func(o *Options) {
		o.DefaultTTL = ttl
	}
}

// WithNullTTL sets the null value TTL.
func WithNullTTL(ttl time.Duration) Option {
	return func(o *Options) {
		o.NullTTL = ttl
	}
}

// WithJitterPercent sets the TTL jitter percentage.
func WithJitterPercent(percent float64) Option {
	return func(o *Options) {
		o.JitterPercent = percent
	}
}

// WithSerializer sets the serializer.
func WithSerializer(s Serializer) Option {
	return func(o *Options) {
		o.Serializer = s
	}
}

// WithHooks sets the observability hooks.
func WithHooks(h Hooks) Option {
	return func(o *Options) {
		o.Hooks = h
	}
}

// WithLocalCache configures the local cache.
func WithLocalCache(size int, ttl time.Duration) Option {
	return func(o *Options) {
		o.LocalCacheSize = size
		o.LocalCacheTTL = ttl
	}
}

// WithoutLocalCache disables the local cache.
func WithoutLocalCache() Option {
	return func(o *Options) {
		o.LocalCacheSize = 0
		o.LocalCacheTTL = 0
	}
}

// WithFallbackOnError enables graceful degradation.
// When Redis encounters a connection error, GetOrSet will call fn() directly
// and Get will return ErrNotFound instead of propagating the error.
func WithFallbackOnError(enabled bool) Option {
	return func(o *Options) {
		o.FallbackOnError = enabled
	}
}

// WithScanBatchSize sets the number of keys to scan per SCAN iteration.
func WithScanBatchSize(n int64) Option {
	return func(o *Options) {
		o.ScanBatchSize = n
	}
}

// WithMaxKeyLength sets the maximum allowed cache key length.
func WithMaxKeyLength(n int) Option {
	return func(o *Options) {
		o.MaxKeyLength = n
	}
}

// WithMaxBatchSize sets the maximum batch size for MGet/MSet/MDelete.
func WithMaxBatchSize(n int) Option {
	return func(o *Options) {
		o.MaxBatchSize = n
	}
}

// WithMaxValueSize sets the maximum allowed serialized value size in bytes for MGet unmarshal.
func WithMaxValueSize(n int) Option {
	return func(o *Options) {
		o.MaxValueSize = n
	}
}

// WithCircuitBreaker configures the circuit breaker.
// When Redis errors exceed the threshold, the circuit opens and requests
// skip Redis entirely until the cooldown expires.
func WithCircuitBreaker(cfg CircuitBreakerConfig) Option {
	return func(o *Options) {
		o.CircuitBreaker = &cfg
	}
}
