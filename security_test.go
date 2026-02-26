package supercache

import (
	"context"
	"crypto/tls"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Fix 1: Key Validation Tests ---

func TestCache_ValidateKey_Empty(t *testing.T) {
	cache, _ := setupTestCache(t)
	ctx := context.Background()

	t.Run("Get empty key", func(t *testing.T) {
		_, err := cache.Get(ctx, "")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("Set empty key", func(t *testing.T) {
		err := cache.Set(ctx, "", testUser{ID: 1})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("SetNull empty key", func(t *testing.T) {
		err := cache.SetNull(ctx, "")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("Delete empty key", func(t *testing.T) {
		err := cache.Delete(ctx, "")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("Exists empty key", func(t *testing.T) {
		_, err := cache.Exists(ctx, "")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("GetOrSet empty key", func(t *testing.T) {
		_, err := cache.GetOrSet(ctx, "", func() (testUser, error) {
			return testUser{}, nil
		})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("GetOrSetPtr empty key", func(t *testing.T) {
		_, err := cache.GetOrSetPtr(ctx, "", func() (*testUser, error) {
			return nil, nil
		})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("GetTTL empty key", func(t *testing.T) {
		_, err := cache.GetTTL(ctx, "")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("Refresh empty key", func(t *testing.T) {
		err := cache.Refresh(ctx, "", time.Minute)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})
}

func TestCache_ValidateKey_TooLong(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithMaxKeyLength(10),
	)
	require.NoError(t, err)

	ctx := context.Background()
	longKey := strings.Repeat("x", 11)

	t.Run("Get key too long", func(t *testing.T) {
		_, err := cache.Get(ctx, longKey)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrKeyTooLong)
	})

	t.Run("Set key too long", func(t *testing.T) {
		err := cache.Set(ctx, longKey, testUser{ID: 1})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrKeyTooLong)
	})

	t.Run("Delete key too long", func(t *testing.T) {
		err := cache.Delete(ctx, longKey)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrKeyTooLong)
	})

	// Verify short key is fine
	t.Run("Short key is ok", func(t *testing.T) {
		shortKey := strings.Repeat("x", 10)
		// Key is valid length, will proceed to Redis call (which we haven't mocked, so will fail)
		_, err := cache.Get(ctx, shortKey)
		// Should NOT be ErrKeyTooLong
		assert.NotErrorIs(t, err, ErrKeyTooLong)
	})
}

func TestCache_MGet_KeyValidation(t *testing.T) {
	cache, _ := setupTestCache(t)
	ctx := context.Background()

	t.Run("MGet with empty key", func(t *testing.T) {
		_, err := cache.MGet(ctx, []string{"valid", ""})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("MSet with empty key", func(t *testing.T) {
		err := cache.MSet(ctx, map[string]testUser{
			"valid": {ID: 1},
			"":      {ID: 2},
		})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("MDelete with empty key", func(t *testing.T) {
		err := cache.MDelete(ctx, []string{"valid", ""})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})
}

// --- Fix 2: Circuit Breaker Mutex Tests ---

func TestCircuitBreaker_ConcurrentRecordSuccessAndFailure(t *testing.T) {
	cb := NewCircuitBreaker(50, 50*time.Millisecond)

	var wg sync.WaitGroup
	// Half goroutines record failures, half record successes
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cb.RecordFailure()
		}()
		go func() {
			defer wg.Done()
			cb.RecordSuccess()
		}()
	}
	wg.Wait()

	// Should complete without race condition — verify state is valid
	state := cb.State()
	assert.Contains(t, []CircuitState{CircuitClosed, CircuitOpen, CircuitHalfOpen}, state)
}

// --- Fix 3: Batch Size Limits Tests ---

func TestCache_MGet_BatchTooLarge(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithMaxBatchSize(5),
	)
	require.NoError(t, err)

	ctx := context.Background()
	keys := make([]string, 6)
	for i := range keys {
		keys[i] = "key" + strings.Repeat("x", i+1)
	}

	_, err = cache.MGet(ctx, keys)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrBatchTooLarge)
}

func TestCache_MSet_BatchTooLarge(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithMaxBatchSize(2),
	)
	require.NoError(t, err)

	ctx := context.Background()
	items := map[string]testUser{
		"a": {ID: 1}, "b": {ID: 2}, "c": {ID: 3},
	}

	err = cache.MSetWithTTL(ctx, items, time.Minute)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrBatchTooLarge)
}

func TestCache_MDelete_BatchTooLarge(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithMaxBatchSize(2),
	)
	require.NoError(t, err)

	ctx := context.Background()
	err = cache.MDelete(ctx, []string{"a", "b", "c"})
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrBatchTooLarge)
}

// --- Fix 4: ClearPattern Context Check Tests ---

func TestCache_ClearPattern_ContextCancelled(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	redisClient := NewRedisClientFromCmdable(rdb)
	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
	)
	require.NoError(t, err)

	// Pre-populate some keys
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		mr.Set("test:item:"+strings.Repeat("x", i+1), "data")
	}

	// Cancel context before calling ClearPattern
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	err = c.ClearPattern(cancelCtx, "item:*")
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// --- Fix 5: Deserialization Size Limit Tests ---

func TestCache_MGet_ValueTooLarge(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	redisClient := NewRedisClientFromCmdable(rdb)

	var hookErrors []string
	hooks := &trackingHooks{}

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithMaxValueSize(10), // Very small limit
		WithHooks(hooks),
	)
	require.NoError(t, err)
	_ = hookErrors

	ctx := context.Background()

	// Store a normal-sized value
	entry := NewEntry(testUser{ID: 1, Name: "John"})
	smallData, _ := MsgPackSerializer{}.Marshal(entry)
	mr.Set("test:small", string(smallData))

	// Store an oversized value
	bigData := strings.Repeat("x", 20)
	mr.Set("test:big", bigData)

	result, err := c.MGet(ctx, []string{"small", "big"})
	require.NoError(t, err)

	// "big" should definitely be skipped due to size limit
	_, hasBig := result["big"]
	assert.False(t, hasBig, "oversized value should be skipped")
	assert.Contains(t, hooks.errors, "big", "should have recorded error for oversized value")
}

// --- Fix 6: TLS Custom Config Tests ---

func TestNewRedisClient_CustomTLSConfig(t *testing.T) {
	customTLS := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: "my-redis.example.com",
	}

	client, err := NewRedisClient(RedisConfig{
		Mode:      RedisModeStandalone,
		Addrs:     []string{"localhost:6379"},
		TLSConfig: customTLS,
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	_ = client.Close()
}

func TestNewRedisClient_CustomTLSConfig_Cluster(t *testing.T) {
	customTLS := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: "my-cluster.example.com",
	}

	client, err := NewRedisClient(RedisConfig{
		Mode:      RedisModeCluster,
		Addrs:     []string{"localhost:7000"},
		TLSConfig: customTLS,
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	_ = client.Close()
}

func TestNewRedisClient_CustomTLSConfig_Failover(t *testing.T) {
	customTLS := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	client, err := NewRedisClient(RedisConfig{
		Mode:       RedisModeFailover,
		Addrs:      []string{"localhost:26379"},
		MasterName: "mymaster",
		TLSConfig:  customTLS,
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	_ = client.Close()
}

// --- Options Validation Tests ---

func TestOptions_Validate_MaxKeyLength(t *testing.T) {
	opts := DefaultOptions()
	opts.MaxKeyLength = 0
	err := opts.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidConfig)
}

func TestOptions_Validate_MaxBatchSize(t *testing.T) {
	opts := DefaultOptions()
	opts.MaxBatchSize = 0
	err := opts.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidConfig)
}

func TestOptions_Validate_MaxValueSize(t *testing.T) {
	opts := DefaultOptions()
	opts.MaxValueSize = 0
	err := opts.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidConfig)
}
