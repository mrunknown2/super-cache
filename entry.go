package supercache

// CacheEntry wraps a cached value with null handling support.
// This helps prevent cache penetration by caching null/empty results.
type CacheEntry[T any] struct {
	// Value holds the actual cached data.
	Value T `json:"v" msgpack:"v"`
	// IsNull indicates if this entry represents a null/not-found value.
	IsNull bool `json:"n" msgpack:"n"`
}

// NewEntry creates a new CacheEntry with the given value.
func NewEntry[T any](value T) CacheEntry[T] {
	return CacheEntry[T]{
		Value:  value,
		IsNull: false,
	}
}

// NewNullEntry creates a CacheEntry representing a null value.
func NewNullEntry[T any]() CacheEntry[T] {
	return CacheEntry[T]{
		IsNull: true,
	}
}

// Get returns the value and whether it's null.
func (e CacheEntry[T]) Get() (T, bool) {
	return e.Value, e.IsNull
}
