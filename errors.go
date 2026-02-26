package supercache

import "errors"

var (
	// ErrNotFound indicates the key does not exist in cache.
	ErrNotFound = errors.New("supercache: key not found")

	// ErrNullValue indicates the cached value is explicitly null.
	ErrNullValue = errors.New("supercache: null value cached")

	// ErrConnection indicates a Redis connection failure.
	ErrConnection = errors.New("supercache: connection failed")

	// ErrSerialization indicates marshal/unmarshal failure.
	ErrSerialization = errors.New("supercache: serialization failed")

	// ErrInvalidConfig indicates invalid configuration.
	ErrInvalidConfig = errors.New("supercache: invalid configuration")

	// ErrClosed indicates the cache has been closed.
	ErrClosed = errors.New("supercache: cache closed")

	// ErrCircuitOpen indicates the circuit breaker is open and requests are being rejected.
	ErrCircuitOpen = errors.New("supercache: circuit breaker is open")

	// ErrKeyTooLong indicates the cache key exceeds MaxKeyLength.
	ErrKeyTooLong = errors.New("supercache: key too long")

	// ErrBatchTooLarge indicates the batch size exceeds MaxBatchSize.
	ErrBatchTooLarge = errors.New("supercache: batch too large")

	// ErrValueTooLarge indicates the value exceeds MaxValueSize.
	ErrValueTooLarge = errors.New("supercache: value too large")
)
