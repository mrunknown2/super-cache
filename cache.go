package supercache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/cache/v9"
)

// Cache is the interface for cache operations.
type Cache[T any] interface {
	// Get retrieves a value from cache.
	Get(ctx context.Context, key string) (T, error)
	// Set stores a value in cache with the default TTL.
	Set(ctx context.Context, key string, value T) error
	// SetWithTTL stores a value in cache with a custom TTL.
	SetWithTTL(ctx context.Context, key string, value T, ttl time.Duration) error
	// SetNull stores a null marker in cache (for cache penetration prevention).
	SetNull(ctx context.Context, key string) error
	// Delete removes a key from cache.
	Delete(ctx context.Context, key string) error
	// Exists checks if a key exists in cache.
	Exists(ctx context.Context, key string) (bool, error)
	// GetOrSet gets a value from cache, or calls fn to load and cache it.
	GetOrSet(ctx context.Context, key string, fn func() (T, error)) (T, error)
	// GetOrSetWithTTL is like GetOrSet but with custom TTL.
	GetOrSetWithTTL(ctx context.Context, key string, ttl time.Duration, fn func() (T, error)) (T, error)
	// GetOrSetPtr gets a value from cache, or calls fn to load it.
	// If fn returns nil, a null entry is cached automatically (prevents cache penetration).
	GetOrSetPtr(ctx context.Context, key string, fn func() (*T, error)) (*T, error)
	// GetOrSetPtrWithTTL is like GetOrSetPtr but with custom TTL.
	GetOrSetPtrWithTTL(ctx context.Context, key string, ttl time.Duration, fn func() (*T, error)) (*T, error)
	// MGet retrieves multiple values from cache.
	MGet(ctx context.Context, keys []string) (map[string]T, error)
	// MSet stores multiple values in cache.
	MSet(ctx context.Context, items map[string]T) error
	// MSetWithTTL stores multiple values with custom TTL.
	MSetWithTTL(ctx context.Context, items map[string]T, ttl time.Duration) error
	// Clear deletes all keys matching the prefix using SCAN.
	Clear(ctx context.Context) error
	// MDelete deletes multiple keys from cache.
	MDelete(ctx context.Context, keys []string) error
	// GetTTL returns the remaining TTL for a key.
	// Returns 0, ErrNotFound if the key does not exist.
	GetTTL(ctx context.Context, key string) (time.Duration, error)
	// ClearPattern deletes all keys matching a pattern using SCAN.
	ClearPattern(ctx context.Context, pattern string) error
	// CircuitBreakerState returns the current circuit breaker state.
	// Returns CircuitClosed if circuit breaker is not enabled.
	CircuitBreakerState() CircuitState
}

// cacheImpl implements Cache[T] using go-redis/cache.
type cacheImpl[T any] struct {
	redis   *RedisClient
	codec   *cache.Cache
	opts    Options
	breaker *CircuitBreaker
	fullKey func(string) string
}

// New creates a new Cache instance.
func New[T any](redisClient *RedisClient, opts ...Option) (Cache[T], error) {
	options := DefaultOptions()
	for _, opt := range opts {
		opt(&options)
	}

	if err := options.Validate(); err != nil {
		return nil, err
	}

	if options.Hooks == nil {
		options.Hooks = NoopHooks{}
	}
	options.Hooks = safeHooks{inner: options.Hooks}

	cacheOpts := &cache.Options{
		Redis: redisClient.Cmdable(),
		Marshal: func(v any) ([]byte, error) {
			return options.Serializer.Marshal(v)
		},
		Unmarshal: func(b []byte, v any) error {
			return options.Serializer.Unmarshal(b, v)
		},
	}

	if options.LocalCacheSize > 0 {
		cacheOpts.LocalCache = cache.NewTinyLFU(options.LocalCacheSize, options.LocalCacheTTL)
	}

	codec := cache.New(cacheOpts)

	var breaker *CircuitBreaker
	if options.CircuitBreaker != nil {
		threshold := options.CircuitBreaker.FailureThreshold
		if threshold <= 0 {
			threshold = 5
		}
		cooldown := options.CircuitBreaker.Cooldown
		if cooldown <= 0 {
			cooldown = 10 * time.Second
		}
		breaker = NewCircuitBreaker(threshold, cooldown)
	}

	return &cacheImpl[T]{
		redis:   redisClient,
		codec:   codec,
		opts:    options,
		breaker: breaker,
		fullKey: func(key string) string {
			return options.KeyPrefix + key
		},
	}, nil
}

func (c *cacheImpl[T]) Get(ctx context.Context, key string) (T, error) {
	var zero T
	fullKey := c.fullKey(key)

	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return zero, ErrNotFound
		}
		return zero, ErrCircuitOpen
	}

	var entry CacheEntry[T]
	err := c.codec.Get(ctx, fullKey, &entry)
	if err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			c.opts.Hooks.OnMiss(ctx, key)
			return zero, ErrNotFound
		}
		c.opts.Hooks.OnError(ctx, key, err)
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		if c.opts.FallbackOnError {
			return zero, ErrNotFound
		}
		return zero, err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}

	if entry.IsNull {
		c.opts.Hooks.OnHit(ctx, key)
		return zero, ErrNullValue
	}

	c.opts.Hooks.OnHit(ctx, key)
	return entry.Value, nil
}

func (c *cacheImpl[T]) Set(ctx context.Context, key string, value T) error {
	return c.SetWithTTL(ctx, key, value, c.opts.DefaultTTL)
}

func (c *cacheImpl[T]) SetWithTTL(ctx context.Context, key string, value T, ttl time.Duration) error {
	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return nil
		}
		return ErrCircuitOpen
	}

	fullKey := c.fullKey(key)
	actualTTL := ttlWithJitter(ttl, c.opts.JitterPercent)

	entry := NewEntry(value)
	err := c.codec.Set(&cache.Item{
		Ctx:   ctx,
		Key:   fullKey,
		Value: entry,
		TTL:   actualTTL,
	})
	if err != nil {
		c.opts.Hooks.OnError(ctx, key, err)
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		return err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}
	c.opts.Hooks.OnSet(ctx, key, actualTTL)
	return nil
}

func (c *cacheImpl[T]) SetNull(ctx context.Context, key string) error {
	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return nil
		}
		return ErrCircuitOpen
	}

	fullKey := c.fullKey(key)
	ttl := ttlWithJitter(c.opts.NullTTL, c.opts.JitterPercent)

	entry := NewNullEntry[T]()
	err := c.codec.Set(&cache.Item{
		Ctx:   ctx,
		Key:   fullKey,
		Value: entry,
		TTL:   ttl,
	})
	if err != nil {
		c.opts.Hooks.OnError(ctx, key, err)
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		return err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}
	c.opts.Hooks.OnSet(ctx, key, ttl)
	return nil
}

func (c *cacheImpl[T]) Delete(ctx context.Context, key string) error {
	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return nil
		}
		return ErrCircuitOpen
	}

	fullKey := c.fullKey(key)
	err := c.codec.Delete(ctx, fullKey)
	if err != nil && !errors.Is(err, cache.ErrCacheMiss) {
		c.opts.Hooks.OnError(ctx, key, err)
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		return err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}
	c.opts.Hooks.OnDelete(ctx, key)
	return nil
}

func (c *cacheImpl[T]) Exists(ctx context.Context, key string) (bool, error) {
	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return false, nil
		}
		return false, ErrCircuitOpen
	}

	fullKey := c.fullKey(key)
	n, err := c.redis.Cmdable().Exists(ctx, fullKey).Result()
	if err != nil {
		c.opts.Hooks.OnError(ctx, key, err)
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		return false, err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}
	return n > 0, nil
}

func (c *cacheImpl[T]) GetOrSet(ctx context.Context, key string, fn func() (T, error)) (T, error) {
	return c.GetOrSetWithTTL(ctx, key, c.opts.DefaultTTL, fn)
}

func (c *cacheImpl[T]) GetOrSetWithTTL(ctx context.Context, key string, ttl time.Duration, fn func() (T, error)) (T, error) {
	var zero T

	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return fn()
		}
		return zero, ErrCircuitOpen
	}

	fullKey := c.fullKey(key)
	actualTTL := ttlWithJitter(ttl, c.opts.JitterPercent)

	var entry CacheEntry[T]
	err := c.codec.Once(&cache.Item{
		Ctx:   ctx,
		Key:   fullKey,
		Value: &entry,
		TTL:   actualTTL,
		Do: func(item *cache.Item) (any, error) {
			value, err := fn()
			if err != nil {
				return nil, err
			}
			return NewEntry(value), nil
		},
	})

	if err != nil {
		c.opts.Hooks.OnError(ctx, key, err)
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		if c.opts.FallbackOnError {
			value, fnErr := fn()
			if fnErr != nil {
				return zero, fnErr
			}
			return value, nil
		}
		return zero, err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}

	if entry.IsNull {
		return zero, ErrNullValue
	}

	return entry.Value, nil
}

func (c *cacheImpl[T]) GetOrSetPtr(ctx context.Context, key string, fn func() (*T, error)) (*T, error) {
	return c.GetOrSetPtrWithTTL(ctx, key, c.opts.DefaultTTL, fn)
}

func (c *cacheImpl[T]) GetOrSetPtrWithTTL(ctx context.Context, key string, ttl time.Duration, fn func() (*T, error)) (*T, error) {
	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return fn()
		}
		return nil, ErrCircuitOpen
	}

	fullKey := c.fullKey(key)
	actualTTL := ttlWithJitter(ttl, c.opts.JitterPercent)

	var entry CacheEntry[T]
	err := c.codec.Once(&cache.Item{
		Ctx:   ctx,
		Key:   fullKey,
		Value: &entry,
		TTL:   actualTTL,
		Do: func(item *cache.Item) (any, error) {
			value, err := fn()
			if err != nil {
				return nil, err
			}
			if value == nil {
				item.TTL = ttlWithJitter(c.opts.NullTTL, c.opts.JitterPercent)
				return NewNullEntry[T](), nil
			}
			return NewEntry(*value), nil
		},
	})

	if err != nil {
		c.opts.Hooks.OnError(ctx, key, err)
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		if c.opts.FallbackOnError {
			value, fnErr := fn()
			if fnErr != nil {
				return nil, fnErr
			}
			return value, nil
		}
		return nil, err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}

	if entry.IsNull {
		return nil, ErrNullValue
	}

	result := entry.Value
	return &result, nil
}

func (c *cacheImpl[T]) MGet(ctx context.Context, keys []string) (map[string]T, error) {
	if len(keys) == 0 {
		return make(map[string]T), nil
	}

	output := make(map[string]T, len(keys))

	// Check local cache first if enabled
	var missKeys []string
	if c.opts.LocalCacheSize > 0 {
		for _, key := range keys {
			fullKey := c.fullKey(key)
			var entry CacheEntry[T]
			err := c.codec.Get(ctx, fullKey, &entry)
			if err == nil {
				if !entry.IsNull {
					output[key] = entry.Value
				}
				continue
			}
			missKeys = append(missKeys, key)
		}
	} else {
		missKeys = keys
	}

	if len(missKeys) == 0 {
		return output, nil
	}

	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return output, nil
		}
		return nil, ErrCircuitOpen
	}

	// Fetch remaining keys from Redis
	fullKeys := make([]string, len(missKeys))
	keyMap := make(map[string]string, len(missKeys))
	for i, key := range missKeys {
		fullKey := c.fullKey(key)
		fullKeys[i] = fullKey
		keyMap[fullKey] = key
	}

	results, err := c.redis.Cmdable().MGet(ctx, fullKeys...).Result()
	if err != nil {
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		if c.opts.FallbackOnError {
			return output, nil
		}
		return nil, err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}

	for i, result := range results {
		if result == nil {
			continue
		}

		data, ok := result.(string)
		if !ok {
			continue
		}

		var entry CacheEntry[T]
		if err := c.opts.Serializer.Unmarshal([]byte(data), &entry); err != nil {
			originalKey := keyMap[fullKeys[i]]
			c.opts.Hooks.OnError(ctx, originalKey, fmt.Errorf("MGet unmarshal key %q: %w", originalKey, err))
			continue
		}

		if !entry.IsNull {
			originalKey := keyMap[fullKeys[i]]
			output[originalKey] = entry.Value

			// Populate local cache from Redis results
			if c.opts.LocalCacheSize > 0 {
				_ = c.codec.Set(&cache.Item{
					Ctx:   ctx,
					Key:   fullKeys[i],
					Value: entry,
					TTL:   c.opts.LocalCacheTTL,
				})
			}
		}
	}

	return output, nil
}

func (c *cacheImpl[T]) MSet(ctx context.Context, items map[string]T) error {
	return c.MSetWithTTL(ctx, items, c.opts.DefaultTTL)
}

func (c *cacheImpl[T]) MSetWithTTL(ctx context.Context, items map[string]T, ttl time.Duration) error {
	if len(items) == 0 {
		return nil
	}

	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return nil
		}
		return ErrCircuitOpen
	}

	type pipeItem struct {
		fullKey string
		data    []byte
		ttl     time.Duration
	}

	// Serialize all items first to avoid partial pipeline on marshal error.
	serialized := make([]pipeItem, 0, len(items))
	for key, value := range items {
		fullKey := c.fullKey(key)
		actualTTL := ttlWithJitter(ttl, c.opts.JitterPercent)

		entry := NewEntry(value)
		data, err := c.opts.Serializer.Marshal(entry)
		if err != nil {
			return fmt.Errorf("MSet marshal key %q: %w", key, err)
		}

		serialized = append(serialized, pipeItem{fullKey: fullKey, data: data, ttl: actualTTL})
	}

	pipe := c.redis.Cmdable().Pipeline()
	for _, item := range serialized {
		pipe.Set(ctx, item.fullKey, item.data, item.ttl)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		return err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}
	return nil
}

func (c *cacheImpl[T]) MDelete(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return nil
		}
		return ErrCircuitOpen
	}

	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = c.fullKey(key)
	}

	err := c.redis.Cmdable().Del(ctx, fullKeys...).Err()
	if err != nil {
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		return err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}
	for _, key := range keys {
		c.opts.Hooks.OnDelete(ctx, key)
	}
	return nil
}

func (c *cacheImpl[T]) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return 0, ErrNotFound
		}
		return 0, ErrCircuitOpen
	}

	fullKey := c.fullKey(key)
	ttl, err := c.redis.Cmdable().TTL(ctx, fullKey).Result()
	if err != nil {
		if c.breaker != nil {
			c.breaker.RecordFailure()
		}
		return 0, err
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}

	// Redis returns -2 when key doesn't exist, -1 when no TTL is set
	if ttl < 0 {
		return 0, ErrNotFound
	}
	return ttl, nil
}

func (c *cacheImpl[T]) Clear(ctx context.Context) error {
	return c.ClearPattern(ctx, "*")
}

func (c *cacheImpl[T]) ClearPattern(ctx context.Context, pattern string) error {
	if c.breaker != nil && !c.breaker.Allow() {
		if c.opts.FallbackOnError {
			return nil
		}
		return ErrCircuitOpen
	}

	fullPattern := c.opts.KeyPrefix + pattern

	var cursor uint64
	for {
		keys, nextCursor, err := c.redis.Scan(ctx, cursor, fullPattern, c.opts.ScanBatchSize)
		if err != nil {
			if c.breaker != nil {
				c.breaker.RecordFailure()
			}
			return err
		}

		if len(keys) > 0 {
			if err := c.redis.Cmdable().Del(ctx, keys...).Err(); err != nil {
				if c.breaker != nil {
					c.breaker.RecordFailure()
				}
				return err
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if c.breaker != nil {
		c.breaker.RecordSuccess()
	}
	return nil
}

func (c *cacheImpl[T]) CircuitBreakerState() CircuitState {
	if c.breaker == nil {
		return CircuitClosed
	}
	return c.breaker.State()
}
