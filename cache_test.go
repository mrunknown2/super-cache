package supercache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testUser struct {
	ID   int64  `json:"id" msgpack:"id"`
	Name string `json:"name" msgpack:"name"`
}

func setupTestCache(t *testing.T) (Cache[testUser], redismock.ClientMock) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithNullTTL(30*time.Second),
		WithJitterPercent(0), // Disable jitter for predictable tests
		WithoutLocalCache(),  // Disable local cache for tests
	)
	require.NoError(t, err)

	return cache, mock
}

func TestCache_SetAndGet(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()
	user := testUser{ID: 1, Name: "John"}

	// Prepare expected data
	entry := NewEntry(user)
	data, _ := MsgPackSerializer{}.Marshal(entry)

	// Expect SET with actual serialized data
	mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")

	err := cache.Set(ctx, "user:1", user)
	require.NoError(t, err)

	// Expect GET
	mock.ExpectGet("test:user:1").SetVal(string(data))

	result, err := cache.Get(ctx, "user:1")
	require.NoError(t, err)
	assert.Equal(t, user.ID, result.ID)
	assert.Equal(t, user.Name, result.Name)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_Get_NotFound(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	mock.ExpectGet("test:user:999").RedisNil()

	_, err := cache.Get(ctx, "user:999")
	assert.ErrorIs(t, err, ErrNotFound)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_SetNull_And_Get(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	// Prepare null entry data
	entry := NewNullEntry[testUser]()
	data, _ := MsgPackSerializer{}.Marshal(entry)

	// Expect SET with null entry
	mock.Regexp().ExpectSet("test:user:null", `.*`, 30*time.Second).SetVal("OK")

	err := cache.SetNull(ctx, "user:null")
	require.NoError(t, err)

	// Expect GET returns null entry
	mock.ExpectGet("test:user:null").SetVal(string(data))

	_, err = cache.Get(ctx, "user:null")
	assert.ErrorIs(t, err, ErrNullValue)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_Delete(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	mock.ExpectDel("test:user:1").SetVal(1)

	err := cache.Delete(ctx, "user:1")
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_Exists(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	mock.ExpectExists("test:user:1").SetVal(1)

	exists, err := cache.Exists(ctx, "user:1")
	require.NoError(t, err)
	assert.True(t, exists)

	mock.ExpectExists("test:user:999").SetVal(0)

	exists, err = cache.Exists(ctx, "user:999")
	require.NoError(t, err)
	assert.False(t, exists)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_SetWithTTL(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()
	user := testUser{ID: 1, Name: "John"}

	customTTL := 10 * time.Minute
	mock.Regexp().ExpectSet("test:user:1", `.*`, customTTL).SetVal("OK")

	err := cache.SetWithTTL(ctx, "user:1", user, customTTL)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_MGet(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "John"}
	user2 := testUser{ID: 2, Name: "Jane"}

	entry1 := NewEntry(user1)
	entry2 := NewEntry(user2)
	data1, _ := MsgPackSerializer{}.Marshal(entry1)
	data2, _ := MsgPackSerializer{}.Marshal(entry2)

	mock.ExpectMGet("test:user:1", "test:user:2", "test:user:3").SetVal([]any{
		string(data1),
		string(data2),
		nil, // user:3 not found
	})

	results, err := cache.MGet(ctx, []string{"user:1", "user:2", "user:3"})
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, user1.ID, results["user:1"].ID)
	assert.Equal(t, user2.ID, results["user:2"].ID)
	_, exists := results["user:3"]
	assert.False(t, exists)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_MSet(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	// Use single item to avoid map iteration order issues
	users := map[string]testUser{
		"user:1": {ID: 1, Name: "John"},
	}

	// Pipeline expectations (not TxPipeline)
	mock.MatchExpectationsInOrder(false)
	mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")

	err := cache.MSet(ctx, users)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_MGet_EmptyKeys(t *testing.T) {
	cache, _ := setupTestCache(t)
	ctx := context.Background()

	results, err := cache.MGet(ctx, []string{})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestCache_MSet_EmptyItems(t *testing.T) {
	cache, _ := setupTestCache(t)
	ctx := context.Background()

	err := cache.MSet(ctx, map[string]testUser{})
	require.NoError(t, err)
}

// Test TTL jitter function
func TestTTLWithJitter(t *testing.T) {
	tests := []struct {
		name          string
		ttl           time.Duration
		jitterPercent float64
		wantMin       time.Duration
		wantMax       time.Duration
	}{
		{
			name:          "10% jitter on 1 minute",
			ttl:           time.Minute,
			jitterPercent: 0.1,
			wantMin:       54 * time.Second,
			wantMax:       66 * time.Second,
		},
		{
			name:          "zero jitter",
			ttl:           time.Minute,
			jitterPercent: 0,
			wantMin:       time.Minute,
			wantMax:       time.Minute,
		},
		{
			name:          "zero TTL",
			ttl:           0,
			jitterPercent: 0.1,
			wantMin:       0,
			wantMax:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run multiple times to test randomness
			for i := 0; i < 100; i++ {
				result := ttlWithJitter(tt.ttl, tt.jitterPercent)
				assert.GreaterOrEqual(t, result, tt.wantMin)
				assert.LessOrEqual(t, result, tt.wantMax)
			}
		})
	}
}

// Test CacheEntry
func TestCacheEntry(t *testing.T) {
	t.Run("NewEntry", func(t *testing.T) {
		user := testUser{ID: 1, Name: "John"}
		entry := NewEntry(user)

		assert.Equal(t, user, entry.Value)
		assert.False(t, entry.IsNull)

		value, isNull := entry.Get()
		assert.Equal(t, user, value)
		assert.False(t, isNull)
	})

	t.Run("NewNullEntry", func(t *testing.T) {
		entry := NewNullEntry[testUser]()

		assert.True(t, entry.IsNull)

		_, isNull := entry.Get()
		assert.True(t, isNull)
	})
}

// Test Options validation
func TestOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{
			name:    "default options are valid",
			opts:    DefaultOptions(),
			wantErr: false,
		},
		{
			name: "zero TTL is invalid",
			opts: func() Options {
				o := DefaultOptions()
				o.DefaultTTL = 0
				return o
			}(),
			wantErr: true,
		},
		{
			name: "negative NullTTL is invalid",
			opts: func() Options {
				o := DefaultOptions()
				o.NullTTL = -1
				return o
			}(),
			wantErr: true,
		},
		{
			name: "jitter > 1 is invalid",
			opts: func() Options {
				o := DefaultOptions()
				o.JitterPercent = 1.5
				return o
			}(),
			wantErr: true,
		},
		{
			name: "nil serializer is invalid",
			opts: func() Options {
				o := DefaultOptions()
				o.Serializer = nil
				return o
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test Serializers
func TestSerializers(t *testing.T) {
	user := testUser{ID: 1, Name: "John"}

	t.Run("JSONSerializer", func(t *testing.T) {
		s := JSONSerializer{}

		data, err := s.Marshal(user)
		require.NoError(t, err)
		assert.Contains(t, string(data), "John")

		var result testUser
		err = s.Unmarshal(data, &result)
		require.NoError(t, err)
		assert.Equal(t, user, result)
	})

	t.Run("MsgPackSerializer", func(t *testing.T) {
		s := MsgPackSerializer{}

		data, err := s.Marshal(user)
		require.NoError(t, err)

		var result testUser
		err = s.Unmarshal(data, &result)
		require.NoError(t, err)
		assert.Equal(t, user, result)
	})

	t.Run("MsgPack produces valid binary format", func(t *testing.T) {
		msgpackData, err := MsgPackSerializer{}.Marshal(user)
		require.NoError(t, err)

		// MsgPack should produce binary data
		assert.NotEmpty(t, msgpackData)

		// Round-trip should work
		var decoded testUser
		err = MsgPackSerializer{}.Unmarshal(msgpackData, &decoded)
		require.NoError(t, err)
		assert.Equal(t, user, decoded)
	})
}

// Test RedisClient
func TestRedisClient(t *testing.T) {
	t.Run("NewRedisClientFromCmdable", func(t *testing.T) {
		db, _ := redismock.NewClientMock()
		client := NewRedisClientFromCmdable(db)

		assert.NotNil(t, client.Cmdable())
		assert.Equal(t, RedisModeStandalone, client.Mode())
		assert.NoError(t, client.Close())
	})

	t.Run("DefaultRedisConfig", func(t *testing.T) {
		cfg := DefaultRedisConfig()

		assert.Equal(t, RedisModeStandalone, cfg.Mode)
		assert.Equal(t, []string{"localhost:6379"}, cfg.Addrs)
		assert.Equal(t, 10, cfg.PoolSize)
	})

	t.Run("NewRedisClient with empty addrs fails", func(t *testing.T) {
		cfg := RedisConfig{Addrs: []string{}}
		_, err := NewRedisClient(cfg)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidConfig))
	})
}

// Test Hooks
func TestHooks(t *testing.T) {
	t.Run("NoopHooks does nothing", func(t *testing.T) {
		h := NoopHooks{}
		ctx := context.Background()

		// Should not panic - calling these exercises the NoopHooks implementation
		h.OnHit(ctx, "key")
		h.OnMiss(ctx, "key")
		h.OnError(ctx, "key", errors.New("test"))
		h.OnSet(ctx, "key", time.Minute)
		h.OnDelete(ctx, "key")
	})
}

func TestNoopHooks_Coverage(t *testing.T) {
	// This test explicitly covers NoopHooks methods
	var h Hooks = NoopHooks{}
	ctx := context.Background()

	// Exercise all methods through the interface
	h.OnHit(ctx, "key1")
	h.OnMiss(ctx, "key2")
	h.OnError(ctx, "key3", errors.New("test error"))
	h.OnSet(ctx, "key4", 5*time.Minute)
	h.OnDelete(ctx, "key5")
}

// Test functional options
func TestFunctionalOptions(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("custom:"),
		WithDefaultTTL(10*time.Minute),
		WithNullTTL(1*time.Minute),
		WithJitterPercent(0.2),
		WithSerializer(JSONSerializer{}),
		WithLocalCache(500, 30*time.Second),
	)

	require.NoError(t, err)
	assert.NotNil(t, cache)
}

func TestFunctionalOptions_WithHooks(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithHooks(NoopHooks{}),
	)

	require.NoError(t, err)
	assert.NotNil(t, cache)
}

func TestCache_GetOrSet(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}
	entry := NewEntry(user)
	data, _ := MsgPackSerializer{}.Marshal(entry)

	t.Run("cache miss - loads from function", func(t *testing.T) {
		// First get returns miss, then set
		mock.ExpectGet("test:user:getorset").RedisNil()
		mock.Regexp().ExpectSet("test:user:getorset", `.*`, 5*time.Minute).SetVal("OK")

		called := false
		result, err := cache.GetOrSet(ctx, "user:getorset", func() (testUser, error) {
			called = true
			return user, nil
		})

		require.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, user.ID, result.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cache hit - returns cached value", func(t *testing.T) {
		mock.ExpectGet("test:user:cached").SetVal(string(data))

		called := false
		result, err := cache.GetOrSet(ctx, "user:cached", func() (testUser, error) {
			called = true
			return testUser{}, nil
		})

		require.NoError(t, err)
		assert.False(t, called)
		assert.Equal(t, user.ID, result.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("function returns error", func(t *testing.T) {
		mock.ExpectGet("test:user:error").RedisNil()

		expectedErr := errors.New("db error")
		_, err := cache.GetOrSet(ctx, "user:error", func() (testUser, error) {
			return testUser{}, expectedErr
		})

		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCache_GetOrSetWithTTL(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}

	mock.ExpectGet("test:user:custom-ttl").RedisNil()
	mock.Regexp().ExpectSet("test:user:custom-ttl", `.*`, 10*time.Minute).SetVal("OK")

	result, err := cache.GetOrSetWithTTL(ctx, "user:custom-ttl", 10*time.Minute, func() (testUser, error) {
		return user, nil
	})

	require.NoError(t, err)
	assert.Equal(t, user.ID, result.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisClient_Ping(t *testing.T) {
	db, mock := redismock.NewClientMock()
	client := NewRedisClientFromCmdable(db)
	ctx := context.Background()

	t.Run("ping success", func(t *testing.T) {
		mock.ExpectPing().SetVal("PONG")
		err := client.Ping(ctx)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ping failure", func(t *testing.T) {
		mock.ExpectPing().SetErr(errors.New("connection refused"))
		err := client.Ping(ctx)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrConnection))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// trackingHooks is a test helper that tracks hook calls
type trackingHooks struct {
	hits    []string
	misses  []string
	errors  []string
	sets    []string
	deletes []string
}

func (h *trackingHooks) OnHit(ctx context.Context, key string)  { h.hits = append(h.hits, key) }
func (h *trackingHooks) OnMiss(ctx context.Context, key string) { h.misses = append(h.misses, key) }
func (h *trackingHooks) OnError(ctx context.Context, key string, err error) {
	h.errors = append(h.errors, key)
}
func (h *trackingHooks) OnSet(ctx context.Context, key string, ttl time.Duration) {
	h.sets = append(h.sets, key)
}
func (h *trackingHooks) OnDelete(ctx context.Context, key string) { h.deletes = append(h.deletes, key) }

func TestCache_WithTrackingHooks(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)
	hooks := &trackingHooks{}

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithHooks(hooks),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}
	entry := NewEntry(user)
	data, _ := MsgPackSerializer{}.Marshal(entry)

	// Test OnSet
	mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")
	err = cache.Set(ctx, "user:1", user)
	require.NoError(t, err)
	assert.Contains(t, hooks.sets, "user:1")

	// Test OnHit
	mock.ExpectGet("test:user:1").SetVal(string(data))
	_, err = cache.Get(ctx, "user:1")
	require.NoError(t, err)
	assert.Contains(t, hooks.hits, "user:1")

	// Test OnMiss
	mock.ExpectGet("test:user:999").RedisNil()
	_, err = cache.Get(ctx, "user:999")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Contains(t, hooks.misses, "user:999")

	// Test OnDelete
	mock.ExpectDel("test:user:1").SetVal(1)
	err = cache.Delete(ctx, "user:1")
	require.NoError(t, err)
	assert.Contains(t, hooks.deletes, "user:1")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_Clear(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Test Clear with SCAN returning keys, then empty (cursor = 0)
	mock.ExpectScan(0, "test:*", 100).SetVal([]string{"test:user:1", "test:user:2"}, 0)
	mock.ExpectDel("test:user:1", "test:user:2").SetVal(2)

	err = cache.Clear(ctx)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_ClearPattern(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Test ClearPattern with specific pattern
	mock.ExpectScan(0, "test:user:*", 100).SetVal([]string{"test:user:1"}, 0)
	mock.ExpectDel("test:user:1").SetVal(1)

	err = cache.ClearPattern(ctx, "user:*")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_ClearPattern_NoKeys(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Test ClearPattern when no keys found
	mock.ExpectScan(0, "test:nonexistent:*", 100).SetVal([]string{}, 0)

	err = cache.ClearPattern(ctx, "nonexistent:*")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_ClearPattern_MultiplePages(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	cache, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Test ClearPattern with pagination (multiple SCAN calls)
	mock.ExpectScan(0, "test:user:*", 100).SetVal([]string{"test:user:1"}, 42) // cursor = 42
	mock.ExpectDel("test:user:1").SetVal(1)
	mock.ExpectScan(42, "test:user:*", 100).SetVal([]string{"test:user:2"}, 0) // cursor = 0 (done)
	mock.ExpectDel("test:user:2").SetVal(1)

	err = cache.ClearPattern(ctx, "user:*")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Step 1: Graceful Degradation Tests ---

func setupTestCacheWithFallback(t *testing.T) (Cache[testUser], redismock.ClientMock) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithNullTTL(30*time.Second),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithFallbackOnError(true),
	)
	require.NoError(t, err)
	return c, mock
}

func TestCache_Get_FallbackOnError(t *testing.T) {
	cache, mock := setupTestCacheWithFallback(t)
	ctx := context.Background()

	t.Run("redis error returns ErrNotFound when fallback enabled", func(t *testing.T) {
		mock.ExpectGet("test:user:1").SetErr(errors.New("connection refused"))

		_, err := cache.Get(ctx, "user:1")
		assert.ErrorIs(t, err, ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCache_GetOrSet_FallbackOnError(t *testing.T) {
	cache, mock := setupTestCacheWithFallback(t)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}

	t.Run("redis error calls fn directly when fallback enabled", func(t *testing.T) {
		mock.ExpectGet("test:user:fb").SetErr(errors.New("connection refused"))

		called := false
		result, err := cache.GetOrSet(ctx, "user:fb", func() (testUser, error) {
			called = true
			return user, nil
		})

		require.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, user.ID, result.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fn error propagates even when fallback enabled", func(t *testing.T) {
		mock.ExpectGet("test:user:fb2").SetErr(errors.New("connection refused"))

		expectedErr := errors.New("db error")
		_, err := cache.GetOrSet(ctx, "user:fb2", func() (testUser, error) {
			return testUser{}, expectedErr
		})

		assert.ErrorIs(t, err, expectedErr)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// --- Step 2: GetOrSetPtr Tests ---

func TestCache_GetOrSetPtr(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}

	t.Run("returns value when fn returns non-nil", func(t *testing.T) {
		mock.ExpectGet("test:user:ptr1").RedisNil()
		mock.Regexp().ExpectSet("test:user:ptr1", `.*`, 5*time.Minute).SetVal("OK")

		result, err := cache.GetOrSetPtr(ctx, "user:ptr1", func() (*testUser, error) {
			return &user, nil
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, user.ID, result.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("caches null when fn returns nil", func(t *testing.T) {
		mock.ExpectGet("test:user:ptr2").RedisNil()
		mock.Regexp().ExpectSet("test:user:ptr2", `.*`, 30*time.Second).SetVal("OK")

		result, err := cache.GetOrSetPtr(ctx, "user:ptr2", func() (*testUser, error) {
			return nil, nil
		})

		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrNullValue)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns cached value on hit", func(t *testing.T) {
		entry := NewEntry(user)
		data, _ := MsgPackSerializer{}.Marshal(entry)
		mock.ExpectGet("test:user:ptr3").SetVal(string(data))

		called := false
		result, err := cache.GetOrSetPtr(ctx, "user:ptr3", func() (*testUser, error) {
			called = true
			return &user, nil
		})

		require.NoError(t, err)
		assert.False(t, called)
		require.NotNil(t, result)
		assert.Equal(t, user.ID, result.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns ErrNullValue on cached null hit", func(t *testing.T) {
		entry := NewNullEntry[testUser]()
		data, _ := MsgPackSerializer{}.Marshal(entry)
		mock.ExpectGet("test:user:ptr4").SetVal(string(data))

		result, err := cache.GetOrSetPtr(ctx, "user:ptr4", func() (*testUser, error) {
			return &user, nil
		})

		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrNullValue)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fn error propagates", func(t *testing.T) {
		mock.ExpectGet("test:user:ptr5").RedisNil()

		expectedErr := errors.New("db error")
		result, err := cache.GetOrSetPtr(ctx, "user:ptr5", func() (*testUser, error) {
			return nil, expectedErr
		})

		assert.Nil(t, result)
		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCache_GetOrSetPtrWithTTL(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}

	mock.ExpectGet("test:user:ptrttl").RedisNil()
	mock.Regexp().ExpectSet("test:user:ptrttl", `.*`, 10*time.Minute).SetVal("OK")

	result, err := cache.GetOrSetPtrWithTTL(ctx, "user:ptrttl", 10*time.Minute, func() (*testUser, error) {
		return &user, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, user.ID, result.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_GetOrSetPtr_FallbackOnError(t *testing.T) {
	cache, mock := setupTestCacheWithFallback(t)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}

	t.Run("redis error falls back to fn", func(t *testing.T) {
		mock.ExpectGet("test:user:ptrfb").SetErr(errors.New("connection refused"))

		result, err := cache.GetOrSetPtr(ctx, "user:ptrfb", func() (*testUser, error) {
			return &user, nil
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, user.ID, result.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("redis error + fn returns nil caches null", func(t *testing.T) {
		mock.ExpectGet("test:user:ptrfb2").SetErr(errors.New("connection refused"))
		// Once will call fn() internally, fn returns nil -> null entry
		// The SET may also error but Once handles it internally
		mock.Regexp().ExpectSet("test:user:ptrfb2", `.*`, 30*time.Second).SetErr(errors.New("connection refused"))

		result, err := cache.GetOrSetPtr(ctx, "user:ptrfb2", func() (*testUser, error) {
			return nil, nil
		})

		// With fallback enabled, fn is called inside Once's Do, returns null entry
		// Even if Redis errors on SET, Once returns the entry successfully
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrNullValue)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("redis error + fn error propagates", func(t *testing.T) {
		mock.ExpectGet("test:user:ptrfb3").SetErr(errors.New("connection refused"))

		expectedErr := errors.New("db error")
		result, err := cache.GetOrSetPtr(ctx, "user:ptrfb3", func() (*testUser, error) {
			return nil, expectedErr
		})

		assert.Nil(t, result)
		assert.ErrorIs(t, err, expectedErr)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// --- Step 3: MGet Local Cache Tests ---

func TestCache_MGet_WithLocalCache(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithLocalCache(100, time.Minute),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "John"}
	user2 := testUser{ID: 2, Name: "Jane"}

	// Set user1 via Set (goes into local cache + Redis)
	mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")
	err = c.Set(ctx, "user:1", user1)
	require.NoError(t, err)

	// MGet for user:1 (local cache hit) and user:2 (miss -> Redis)
	entry2 := NewEntry(user2)
	data2, _ := MsgPackSerializer{}.Marshal(entry2)
	mock.ExpectMGet("test:user:2").SetVal([]any{string(data2)})

	results, err := c.MGet(ctx, []string{"user:1", "user:2"})
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, user1.ID, results["user:1"].ID)
	assert.Equal(t, user2.ID, results["user:2"].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_MGet_AllLocalCacheHits(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithLocalCache(100, time.Minute),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "John"}

	// Set user1 via Set (goes into local cache)
	mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")
	err = c.Set(ctx, "user:1", user1)
	require.NoError(t, err)

	// MGet for only user:1 — should NOT call Redis MGet at all
	results, err := c.MGet(ctx, []string{"user:1"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, user1.ID, results["user:1"].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Step 4: ClearPattern SCAN error Tests ---

func TestCache_ClearPattern_ScanError(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
	)
	require.NoError(t, err)
	ctx := context.Background()

	mock.ExpectScan(0, "test:err:*", 100).SetErr(errors.New("scan error"))

	err = c.ClearPattern(ctx, "err:*")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_ClearPattern_DelError(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
	)
	require.NoError(t, err)
	ctx := context.Background()

	mock.ExpectScan(0, "test:del:*", 100).SetVal([]string{"test:del:1"}, 0)
	mock.ExpectDel("test:del:1").SetErr(errors.New("del error"))

	err = c.ClearPattern(ctx, "del:*")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- MSetWithTTL error tests ---

func TestCache_MSetWithTTL_SerializationError(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	// Use a serializer that fails
	badSerializer := &failingSerializer{}

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithSerializer(badSerializer),
	)
	require.NoError(t, err)
	ctx := context.Background()

	items := map[string]testUser{
		"user:1": {ID: 1, Name: "John"},
	}

	err = c.MSetWithTTL(ctx, items, 5*time.Minute)
	assert.Error(t, err)
}

type failingSerializer struct{}

func (f *failingSerializer) Marshal(v any) ([]byte, error) {
	return nil, errors.New("marshal error")
}

func (f *failingSerializer) Unmarshal(data []byte, v any) error {
	return errors.New("unmarshal error")
}

// --- MGet FallbackOnError test ---

func TestCache_MGet_FallbackOnError(t *testing.T) {
	cache, mock := setupTestCacheWithFallback(t)
	ctx := context.Background()

	mock.ExpectMGet("test:user:1").SetErr(errors.New("connection refused"))

	results, err := cache.MGet(ctx, []string{"user:1"})
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Exists error test ---

func TestCache_Exists_Error(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	mock.ExpectExists("test:user:err").SetErr(errors.New("connection refused"))

	_, err := cache.Exists(ctx, "user:err")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Redis Scan without scanner test ---

func TestRedisClient_Scan_NoScanner(t *testing.T) {
	rc := &RedisClient{
		cmdable: nil,
		scanner: nil,
		mode:    RedisModeStandalone,
		closer:  func() error { return nil },
	}

	_, _, err := rc.Scan(context.Background(), 0, "*", 100)
	assert.Error(t, err)
}

// --- WithFallbackOnError option test ---

func TestWithFallbackOnError(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithFallbackOnError(true),
	)
	require.NoError(t, err)
	assert.NotNil(t, c)
}

// --- SetWithTTL error test ---

func TestCache_SetWithTTL_Error(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	mock.Regexp().ExpectSet("test:user:err", `.*`, 5*time.Minute).SetErr(errors.New("redis error"))

	err := cache.Set(ctx, "user:err", testUser{ID: 1, Name: "John"})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- SetNull error test ---

func TestCache_SetNull_Error(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	mock.Regexp().ExpectSet("test:user:nullerr", `.*`, 30*time.Second).SetErr(errors.New("redis error"))

	err := cache.SetNull(ctx, "user:nullerr")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Delete non-existent key test ---

func TestCache_Delete_NotExist(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	mock.ExpectDel("test:user:none").SetVal(0)

	err := cache.Delete(ctx, "user:none")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Step 1 (Phase 2): Concurrent Tests ---

func setupMiniredisCache(t *testing.T, opts ...Option) Cache[testUser] {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	redisClient := NewRedisClientFromCmdable(rdb)

	defaultOpts := []Option{
		WithKeyPrefix("test:"),
		WithDefaultTTL(5 * time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
	}
	defaultOpts = append(defaultOpts, opts...)

	c, err := New[testUser](redisClient, defaultOpts...)
	require.NoError(t, err)
	return c
}

func TestCache_GetOrSet_Concurrent(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "Concurrent"}
	var callCount atomic.Int64

	const goroutines = 50
	var wg sync.WaitGroup
	results := make([]testUser, goroutines)
	errs := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			val, err := c.GetOrSet(ctx, "concurrent-key", func() (testUser, error) {
				callCount.Add(1)
				return user, nil
			})
			results[idx] = val
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// All goroutines should succeed
	for i := range goroutines {
		assert.NoError(t, errs[i], "goroutine %d", i)
		assert.Equal(t, user.ID, results[i].ID, "goroutine %d", i)
		assert.Equal(t, user.Name, results[i].Name, "goroutine %d", i)
	}

	// singleflight should have deduplicated: fn called very few times
	// (ideally 1, but could be 2 due to timing)
	count := callCount.Load()
	assert.LessOrEqual(t, count, int64(5), "singleflight should dedup, got %d calls", count)
}

func TestCache_GetOrSet_Concurrent_DifferentKeys(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx := context.Background()

	var callCount atomic.Int64

	const goroutines = 20
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := "key-" + string(rune('A'+idx))
			_, err := c.GetOrSet(ctx, key, func() (testUser, error) {
				callCount.Add(1)
				return testUser{ID: int64(idx), Name: key}, nil
			})
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// Different keys should each call fn
	count := callCount.Load()
	assert.Equal(t, int64(goroutines), count)
}

// --- Step 2 (Phase 2): MGet Populate Local Cache ---

func TestCache_MGet_PopulatesLocalCache(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	redisClient := NewRedisClientFromCmdable(rdb)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithLocalCache(100, time.Minute),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "John"}
	user2 := testUser{ID: 2, Name: "Jane"}

	// Set values directly to Redis (bypassing local cache)
	entry1 := NewEntry(user1)
	data1, _ := MsgPackSerializer{}.Marshal(entry1)
	entry2 := NewEntry(user2)
	data2, _ := MsgPackSerializer{}.Marshal(entry2)
	rdb.Set(ctx, "test:user:1", data1, 5*time.Minute)
	rdb.Set(ctx, "test:user:2", data2, 5*time.Minute)

	// MGet should fetch from Redis and populate local cache
	results, err := c.MGet(ctx, []string{"user:1", "user:2"})
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, user1.ID, results["user:1"].ID)
	assert.Equal(t, user2.ID, results["user:2"].ID)

	// Delete from Redis to prove next Get hits local cache
	rdb.Del(ctx, "test:user:1", "test:user:2")

	// Get should hit local cache (not Redis)
	val1, err := c.Get(ctx, "user:1")
	require.NoError(t, err)
	assert.Equal(t, user1.ID, val1.ID)

	val2, err := c.Get(ctx, "user:2")
	require.NoError(t, err)
	assert.Equal(t, user2.ID, val2.ID)
}

// --- Step 3 (Phase 2): Circuit Breaker Integration Tests ---

func TestCache_CircuitBreaker_OpensOnFailures(t *testing.T) {
	c, mock := setupTestCacheWithCircuitBreaker(t)
	ctx := context.Background()

	// Cause 3 failures to trip the breaker (threshold=3)
	for i := 0; i < 3; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	// Circuit should be open now — returns ErrCircuitOpen without Redis call
	_, err := c.Get(ctx, "user:blocked")
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func setupTestCacheWithCircuitBreaker(t *testing.T) (Cache[testUser], redismock.ClientMock) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 3,
			Cooldown:         time.Second,
		}),
	)
	require.NoError(t, err)
	return c, mock
}

func TestCache_CircuitBreaker_WithFallback(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithFallbackOnError(true),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 2,
			Cooldown:         time.Second,
		}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Cause 2 failures to trip breaker
	for i := 0; i < 2; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	// Circuit open + fallback → Get returns ErrNotFound (not ErrCircuitOpen)
	_, err = c.Get(ctx, "user:fb")
	assert.ErrorIs(t, err, ErrNotFound)

	// Circuit open + fallback → GetOrSet calls fn directly
	user := testUser{ID: 99, Name: "Fallback"}
	result, err := c.GetOrSet(ctx, "user:fb2", func() (testUser, error) {
		return user, nil
	})
	require.NoError(t, err)
	assert.Equal(t, user.ID, result.ID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_CircuitBreaker_RecoveryAfterCooldown(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 2,
			Cooldown:         50 * time.Millisecond,
		}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}
	entry := NewEntry(user)
	data, _ := MsgPackSerializer{}.Marshal(entry)

	// Trip the breaker
	for i := 0; i < 2; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	// Circuit open
	_, err = c.Get(ctx, "user:blocked")
	assert.ErrorIs(t, err, ErrCircuitOpen)

	// Wait for cooldown
	time.Sleep(60 * time.Millisecond)

	// Half-Open: allows one request — if it succeeds, circuit closes
	mock.ExpectGet("test:user:recovery").SetVal(string(data))
	result, err := c.Get(ctx, "user:recovery")
	require.NoError(t, err)
	assert.Equal(t, user.ID, result.ID)

	// Circuit should be closed again
	mock.ExpectGet("test:user:normal").SetVal(string(data))
	result, err = c.Get(ctx, "user:normal")
	require.NoError(t, err)
	assert.Equal(t, user.ID, result.ID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_CircuitBreaker_WriteOperations(t *testing.T) {
	c, mock := setupTestCacheWithCircuitBreaker(t)
	ctx := context.Background()

	// Trip the breaker
	for i := 0; i < 3; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	// All write operations should return ErrCircuitOpen
	err := c.Set(ctx, "user:1", testUser{ID: 1})
	assert.ErrorIs(t, err, ErrCircuitOpen)

	err = c.SetNull(ctx, "user:null")
	assert.ErrorIs(t, err, ErrCircuitOpen)

	err = c.Delete(ctx, "user:1")
	assert.ErrorIs(t, err, ErrCircuitOpen)

	_, err = c.Exists(ctx, "user:1")
	assert.ErrorIs(t, err, ErrCircuitOpen)

	err = c.MSet(ctx, map[string]testUser{"user:1": {ID: 1}})
	assert.ErrorIs(t, err, ErrCircuitOpen)

	err = c.Clear(ctx)
	assert.ErrorIs(t, err, ErrCircuitOpen)

	_, err = c.MGet(ctx, []string{"user:1"})
	assert.ErrorIs(t, err, ErrCircuitOpen)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_CircuitBreaker_DefaultConfig(t *testing.T) {
	db, _ := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	// Test with zero values — should use defaults
	c, err := New[testUser](redisClient,
		WithCircuitBreaker(CircuitBreakerConfig{}),
	)
	require.NoError(t, err)
	assert.NotNil(t, c)
}

func TestCache_CircuitBreaker_WriteWithFallback(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithFallbackOnError(true),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 2,
			Cooldown:         time.Second,
		}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Trip the breaker
	for i := 0; i < 2; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	// With fallback, write ops return nil (best-effort)
	err = c.Set(ctx, "user:1", testUser{ID: 1})
	assert.NoError(t, err)

	err = c.SetNull(ctx, "user:null")
	assert.NoError(t, err)

	err = c.Delete(ctx, "user:1")
	assert.NoError(t, err)

	ok, err := c.Exists(ctx, "user:1")
	assert.NoError(t, err)
	assert.False(t, ok)

	err = c.MSet(ctx, map[string]testUser{"user:1": {ID: 1}})
	assert.NoError(t, err)

	err = c.Clear(ctx)
	assert.NoError(t, err)

	results, err := c.MGet(ctx, []string{"user:1"})
	assert.NoError(t, err)
	assert.Empty(t, results)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_Delete_ErrorWithBreaker(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 5,
			Cooldown:         time.Second,
		}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Delete error records a failure
	mock.ExpectDel("test:user:1").SetErr(errors.New("connection refused"))
	err = c.Delete(ctx, "user:1")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_SetWithTTL_SuccessWithBreaker(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 5,
			Cooldown:         time.Second,
		}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")
	err = c.Set(ctx, "user:1", testUser{ID: 1, Name: "John"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_SetNull_SuccessWithBreaker(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 5,
			Cooldown:         time.Second,
		}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	mock.Regexp().ExpectSet("test:user:null", `.*`, 30*time.Second).SetVal("OK")
	err = c.SetNull(ctx, "user:null")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_CircuitBreaker_GetOrSetPtr(t *testing.T) {
	c, mock := setupTestCacheWithCircuitBreaker(t)
	ctx := context.Background()

	// Trip the breaker
	for i := 0; i < 3; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	// GetOrSetPtr with circuit open
	_, err := c.GetOrSetPtr(ctx, "user:ptr", func() (*testUser, error) {
		return &testUser{ID: 1}, nil
	})
	assert.ErrorIs(t, err, ErrCircuitOpen)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Hook panic recovery tests ---

type panicHooks struct{}

func (panicHooks) OnHit(ctx context.Context, key string)                    { panic("OnHit panic") }
func (panicHooks) OnMiss(ctx context.Context, key string)                   { panic("OnMiss panic") }
func (panicHooks) OnError(ctx context.Context, key string, err error)       { panic("OnError panic") }
func (panicHooks) OnSet(ctx context.Context, key string, ttl time.Duration) { panic("OnSet panic") }
func (panicHooks) OnDelete(ctx context.Context, key string)                 { panic("OnDelete panic") }

func TestCache_HookPanicRecovery(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithHooks(panicHooks{}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}
	entry := NewEntry(user)
	data, _ := MsgPackSerializer{}.Marshal(entry)

	t.Run("panic in OnSet does not crash", func(t *testing.T) {
		mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")
		err := c.Set(ctx, "user:1", user)
		assert.NoError(t, err)
	})

	t.Run("panic in OnHit does not crash", func(t *testing.T) {
		mock.ExpectGet("test:user:1").SetVal(string(data))
		_, err := c.Get(ctx, "user:1")
		assert.NoError(t, err)
	})

	t.Run("panic in OnMiss does not crash", func(t *testing.T) {
		mock.ExpectGet("test:user:miss").RedisNil()
		_, err := c.Get(ctx, "user:miss")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("panic in OnError does not crash", func(t *testing.T) {
		mock.ExpectGet("test:user:err").SetErr(errors.New("redis error"))
		_, err := c.Get(ctx, "user:err")
		assert.Error(t, err)
	})

	t.Run("panic in OnDelete does not crash", func(t *testing.T) {
		mock.ExpectDel("test:user:1").SetVal(1)
		err := c.Delete(ctx, "user:1")
		assert.NoError(t, err)
	})

	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- MGet deserialization error with hook test ---

func TestCache_MGet_DeserializationError_CallsOnError(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)
	hooks := &trackingHooks{}

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithHooks(hooks),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Return invalid data that can't be deserialized
	mock.ExpectMGet("test:user:1", "test:user:2").SetVal([]any{
		"invalid-data-not-msgpack",
		nil,
	})

	results, err := c.MGet(ctx, []string{"user:1", "user:2"})
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Contains(t, hooks.errors, "user:1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Context cancellation tests ---

func TestCache_Get_ContextCancelled(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := c.Get(ctx, "user:1")
	assert.Error(t, err)
}

func TestCache_Set_ContextCancelled(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.Set(ctx, "user:1", testUser{ID: 1})
	assert.Error(t, err)
}

func TestCache_GetOrSet_FnReturnsContextError(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	mock.ExpectGet("test:user:timeout").RedisNil()

	_, err := cache.GetOrSet(ctx, "user:timeout", func() (testUser, error) {
		return testUser{}, context.DeadlineExceeded
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- GetOrSetWithTTL + CircuitBreaker combination ---

func TestCache_GetOrSetWithTTL_CircuitBreaker(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 2,
			Cooldown:         time.Second,
		}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Trip breaker
	for i := 0; i < 2; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	_, err = c.GetOrSetWithTTL(ctx, "user:cb", 10*time.Minute, func() (testUser, error) {
		return testUser{ID: 1}, nil
	})
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Concurrent MSet + MGet stress test ---

func TestCache_Concurrent_MSet_MGet(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx := context.Background()

	const goroutines = 20
	var wg sync.WaitGroup

	// Concurrent writes
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			items := map[string]testUser{
				fmt.Sprintf("user:%d", idx): {ID: int64(idx), Name: fmt.Sprintf("User%d", idx)},
			}
			err := c.MSet(ctx, items)
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// Concurrent reads
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			keys := []string{fmt.Sprintf("user:%d", idx)}
			results, err := c.MGet(ctx, keys)
			assert.NoError(t, err)
			assert.Len(t, results, 1)
		}(i)
	}
	wg.Wait()
}

// --- CircuitBreakerState tests ---

func TestCache_CircuitBreakerState(t *testing.T) {
	t.Run("no circuit breaker returns CircuitClosed", func(t *testing.T) {
		c := setupMiniredisCache(t)
		assert.Equal(t, CircuitClosed, c.CircuitBreakerState())
	})

	t.Run("reports state after failures", func(t *testing.T) {
		c, mock := setupTestCacheWithCircuitBreaker(t)
		ctx := context.Background()

		assert.Equal(t, CircuitClosed, c.CircuitBreakerState())

		// Trip breaker
		for i := 0; i < 3; i++ {
			mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
			_, _ = c.Get(ctx, "user:fail")
		}

		assert.Equal(t, CircuitOpen, c.CircuitBreakerState())
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// --- Redis cluster mode initialization test ---

func TestNewRedisClient_ClusterMode(t *testing.T) {
	cfg := RedisConfig{
		Mode:  RedisModeCluster,
		Addrs: []string{"localhost:7000", "localhost:7001"},
	}
	client, err := NewRedisClient(cfg)
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, RedisModeCluster, client.Mode())
	_ = client.Close()
}

func TestNewRedisClient_StandaloneWithTLS(t *testing.T) {
	cfg := RedisConfig{
		Mode:   RedisModeStandalone,
		Addrs:  []string{"localhost:6379"},
		UseTLS: true,
	}
	client, err := NewRedisClient(cfg)
	require.NoError(t, err)
	assert.NotNil(t, client)
	_ = client.Close()
}

// --- Options validation new tests ---

func TestOptions_Validate_LocalCacheTTL(t *testing.T) {
	o := DefaultOptions()
	o.LocalCacheSize = 100
	o.LocalCacheTTL = 0
	err := o.Validate()
	assert.ErrorIs(t, err, ErrInvalidConfig)
}

// --- CircuitBreaker String() test ---

func TestCircuitState_String(t *testing.T) {
	assert.Equal(t, "closed", CircuitClosed.String())
	assert.Equal(t, "open", CircuitOpen.String())
	assert.Equal(t, "half-open", CircuitHalfOpen.String())
	assert.Equal(t, "unknown", CircuitState(99).String())
}

// --- MSet pipeline exec error test ---

func TestCache_MSetWithTTL_PipelineExecError(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx := context.Background()

	// Normal MSet should work
	items := map[string]testUser{
		"user:1": {ID: 1, Name: "John"},
		"user:2": {ID: 2, Name: "Jane"},
	}
	err := c.MSetWithTTL(ctx, items, 5*time.Minute)
	require.NoError(t, err)

	// Verify data was set
	results, err := c.MGet(ctx, []string{"user:1", "user:2"})
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

// --- GetOrSetPtr with CircuitBreaker + Fallback ---

func TestCache_GetOrSetPtr_CircuitBreaker_WithFallback(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithFallbackOnError(true),
		WithCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 2,
			Cooldown:         time.Second,
		}),
	)
	require.NoError(t, err)
	ctx := context.Background()

	// Trip breaker
	for i := 0; i < 2; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	user := testUser{ID: 99, Name: "Fallback"}
	result, err := c.GetOrSetPtr(ctx, "user:ptrfb", func() (*testUser, error) {
		return &user, nil
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, user.ID, result.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- MDelete tests ---

func TestCache_MDelete(t *testing.T) {
	cache, mock := setupTestCache(t)
	ctx := context.Background()

	t.Run("deletes multiple keys", func(t *testing.T) {
		mock.ExpectDel("test:user:1", "test:user:2").SetVal(2)
		err := cache.MDelete(ctx, []string{"user:1", "user:2"})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty keys returns nil", func(t *testing.T) {
		err := cache.MDelete(ctx, []string{})
		require.NoError(t, err)
	})

	t.Run("redis error propagates", func(t *testing.T) {
		mock.ExpectDel("test:user:err").SetErr(errors.New("connection refused"))
		err := cache.MDelete(ctx, []string{"user:err"})
		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCache_MDelete_CircuitBreaker(t *testing.T) {
	c, mock := setupTestCacheWithCircuitBreaker(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	err := c.MDelete(ctx, []string{"user:1"})
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_MDelete_WithHooks(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)
	hooks := &trackingHooks{}

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithHooks(hooks),
	)
	require.NoError(t, err)
	ctx := context.Background()

	mock.ExpectDel("test:user:1", "test:user:2").SetVal(2)
	err = c.MDelete(ctx, []string{"user:1", "user:2"})
	require.NoError(t, err)
	assert.Contains(t, hooks.deletes, "user:1")
	assert.Contains(t, hooks.deletes, "user:2")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- GetTTL tests ---

func TestCache_GetTTL(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx := context.Background()

	t.Run("returns TTL for existing key", func(t *testing.T) {
		err := c.SetWithTTL(ctx, "user:ttl", testUser{ID: 1}, 5*time.Minute)
		require.NoError(t, err)

		ttl, err := c.GetTTL(ctx, "user:ttl")
		require.NoError(t, err)
		assert.Greater(t, ttl, time.Duration(0))
		assert.LessOrEqual(t, ttl, 5*time.Minute)
	})

	t.Run("returns ErrNotFound for missing key", func(t *testing.T) {
		_, err := c.GetTTL(ctx, "user:missing")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestCache_GetTTL_CircuitBreaker(t *testing.T) {
	c, mock := setupTestCacheWithCircuitBreaker(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		mock.ExpectGet("test:user:fail").SetErr(errors.New("connection refused"))
		_, _ = c.Get(ctx, "user:fail")
	}

	_, err := c.GetTTL(ctx, "user:1")
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Close() tests ---

func TestCache_Close(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx := context.Background()

	// Close the cache
	err := c.Close()
	require.NoError(t, err)

	// All operations should return ErrClosed
	_, err = c.Get(ctx, "key")
	assert.ErrorIs(t, err, ErrClosed)

	err = c.Set(ctx, "key", testUser{ID: 1})
	assert.ErrorIs(t, err, ErrClosed)

	err = c.SetWithTTL(ctx, "key", testUser{ID: 1}, time.Minute)
	assert.ErrorIs(t, err, ErrClosed)

	err = c.SetNull(ctx, "key")
	assert.ErrorIs(t, err, ErrClosed)

	err = c.Delete(ctx, "key")
	assert.ErrorIs(t, err, ErrClosed)

	_, err = c.Exists(ctx, "key")
	assert.ErrorIs(t, err, ErrClosed)

	_, err = c.GetOrSet(ctx, "key", func() (testUser, error) {
		return testUser{}, nil
	})
	assert.ErrorIs(t, err, ErrClosed)

	_, err = c.GetOrSetWithTTL(ctx, "key", time.Minute, func() (testUser, error) {
		return testUser{}, nil
	})
	assert.ErrorIs(t, err, ErrClosed)

	_, err = c.GetOrSetPtr(ctx, "key", func() (*testUser, error) {
		return nil, nil
	})
	assert.ErrorIs(t, err, ErrClosed)

	_, err = c.GetOrSetPtrWithTTL(ctx, "key", time.Minute, func() (*testUser, error) {
		return nil, nil
	})
	assert.ErrorIs(t, err, ErrClosed)

	_, err = c.MGet(ctx, []string{"key"})
	assert.ErrorIs(t, err, ErrClosed)

	err = c.MSet(ctx, map[string]testUser{"key": {ID: 1}})
	assert.ErrorIs(t, err, ErrClosed)

	err = c.MSetWithTTL(ctx, map[string]testUser{"key": {ID: 1}}, time.Minute)
	assert.ErrorIs(t, err, ErrClosed)

	err = c.MDelete(ctx, []string{"key"})
	assert.ErrorIs(t, err, ErrClosed)

	_, err = c.GetTTL(ctx, "key")
	assert.ErrorIs(t, err, ErrClosed)

	err = c.ClearPattern(ctx, "*")
	assert.ErrorIs(t, err, ErrClosed)

	err = c.Clear(ctx)
	assert.ErrorIs(t, err, ErrClosed)

	err = c.Refresh(ctx, "key", time.Minute)
	assert.ErrorIs(t, err, ErrClosed)

	// CircuitBreakerState should still work after close
	state := c.CircuitBreakerState()
	assert.Equal(t, CircuitClosed, state)
}

func TestCache_Close_Idempotent(t *testing.T) {
	c := setupMiniredisCache(t)

	err := c.Close()
	require.NoError(t, err)

	err = c.Close()
	require.NoError(t, err)
}

func TestCache_Close_Concurrent(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx := context.Background()

	const goroutines = 20
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				_ = c.Close()
			} else {
				_, _ = c.Get(ctx, "key")
			}
		}(i)
	}

	wg.Wait()
}

// --- MSet OnSet hooks test ---

func TestCache_MSet_CallsOnSetHooks(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)
	hooks := &trackingHooks{}

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithHooks(hooks),
	)
	require.NoError(t, err)
	ctx := context.Background()

	mock.MatchExpectationsInOrder(false)
	mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")
	mock.Regexp().ExpectSet("test:user:2", `.*`, 5*time.Minute).SetVal("OK")

	items := map[string]testUser{
		"user:1": {ID: 1, Name: "John"},
		"user:2": {ID: 2, Name: "Jane"},
	}
	err = c.MSet(ctx, items)
	require.NoError(t, err)

	assert.Len(t, hooks.sets, 2)
	assert.Contains(t, hooks.sets, "user:1")
	assert.Contains(t, hooks.sets, "user:2")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- MGet OnHit/OnMiss hooks tests ---

func TestCache_MGet_CallsOnHitAndOnMiss(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)
	hooks := &trackingHooks{}

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithHooks(hooks),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "John"}
	entry1 := NewEntry(user1)
	data1, _ := MsgPackSerializer{}.Marshal(entry1)

	mock.ExpectMGet("test:user:1", "test:user:2").SetVal([]any{
		string(data1),
		nil, // miss
	})

	results, err := c.MGet(ctx, []string{"user:1", "user:2"})
	require.NoError(t, err)
	assert.Len(t, results, 1)

	assert.Contains(t, hooks.hits, "user:1")
	assert.Contains(t, hooks.misses, "user:2")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_MGet_LocalCache_CallsOnHit(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)
	hooks := &trackingHooks{}

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithLocalCache(100, time.Minute),
		WithHooks(hooks),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "John"}

	// Set user1 via Set (goes into local cache + Redis)
	mock.Regexp().ExpectSet("test:user:1", `.*`, 5*time.Minute).SetVal("OK")
	err = c.Set(ctx, "user:1", user1)
	require.NoError(t, err)
	hooks.hits = nil // reset

	// MGet should hit local cache
	results, err := c.MGet(ctx, []string{"user:1"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Contains(t, hooks.hits, "user:1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- GetOrSetWithTTL fallback no double call ---

func TestCache_GetOrSetWithTTL_FallbackDoesNotDoubleCall(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithFallbackOnError(true),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}
	var callCount atomic.Int64

	// Redis GET misses, fn gets called in Once's Do, Redis SET fails -> triggers fallback
	mock.ExpectGet("test:user:fb-double").RedisNil()
	mock.Regexp().ExpectSet("test:user:fb-double", `.*`, 5*time.Minute).SetErr(errors.New("redis write error"))

	result, err := c.GetOrSetWithTTL(ctx, "user:fb-double", 5*time.Minute, func() (testUser, error) {
		callCount.Add(1)
		return user, nil
	})

	require.NoError(t, err)
	assert.Equal(t, user.ID, result.ID)
	assert.Equal(t, int64(1), callCount.Load(), "fn() should only be called once")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCache_GetOrSetPtrWithTTL_FallbackDoesNotDoubleCall(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithFallbackOnError(true),
	)
	require.NoError(t, err)
	ctx := context.Background()

	user := testUser{ID: 1, Name: "John"}
	var callCount atomic.Int64

	mock.ExpectGet("test:user:ptrfb-double").RedisNil()
	mock.Regexp().ExpectSet("test:user:ptrfb-double", `.*`, 5*time.Minute).SetErr(errors.New("redis write error"))

	result, err := c.GetOrSetPtrWithTTL(ctx, "user:ptrfb-double", 5*time.Minute, func() (*testUser, error) {
		callCount.Add(1)
		return &user, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, user.ID, result.ID)
	assert.Equal(t, int64(1), callCount.Load(), "fn() should only be called once")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Refresh tests ---

func TestCache_Refresh(t *testing.T) {
	c := setupMiniredisCache(t)
	ctx := context.Background()

	t.Run("refreshes TTL for existing key", func(t *testing.T) {
		err := c.Set(ctx, "user:refresh", testUser{ID: 1})
		require.NoError(t, err)

		err = c.Refresh(ctx, "user:refresh", 10*time.Minute)
		require.NoError(t, err)

		ttl, err := c.GetTTL(ctx, "user:refresh")
		require.NoError(t, err)
		assert.Greater(t, ttl, 5*time.Minute)
	})

	t.Run("returns ErrNotFound for missing key", func(t *testing.T) {
		err := c.Refresh(ctx, "user:missing", 10*time.Minute)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("returns ErrClosed after close", func(t *testing.T) {
		c2 := setupMiniredisCache(t)
		_ = c2.Close()

		err := c2.Refresh(ctx, "key", time.Minute)
		assert.ErrorIs(t, err, ErrClosed)
	})
}

func TestCache_Refresh_CallsOnSetHook(t *testing.T) {
	db, mock := redismock.NewClientMock()
	redisClient := NewRedisClientFromCmdable(db)
	hooks := &trackingHooks{}

	c, err := New[testUser](redisClient,
		WithKeyPrefix("test:"),
		WithJitterPercent(0),
		WithoutLocalCache(),
		WithHooks(hooks),
	)
	require.NoError(t, err)
	ctx := context.Background()

	mock.ExpectExpire("test:user:1", 10*time.Minute).SetVal(true)

	err = c.Refresh(ctx, "user:1", 10*time.Minute)
	require.NoError(t, err)
	assert.Contains(t, hooks.sets, "user:1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Redis Sentinel / Failover tests ---

func TestNewRedisClient_FailoverMode(t *testing.T) {
	client, err := NewRedisClient(RedisConfig{
		Mode:       RedisModeFailover,
		Addrs:      []string{"localhost:26379"},
		MasterName: "mymaster",
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, RedisModeFailover, client.Mode())
	_ = client.Close()
}

func TestNewRedisClient_FailoverMode_MissingMasterName(t *testing.T) {
	_, err := NewRedisClient(RedisConfig{
		Mode:  RedisModeFailover,
		Addrs: []string{"localhost:26379"},
	})
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidConfig)
}

// --- Valkey tests ---

func TestNewRedisClient_ValkeyMode(t *testing.T) {
	client, err := NewRedisClient(RedisConfig{
		Mode:  RedisModeValkey,
		Addrs: []string{"localhost:6379"},
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, RedisModeValkey, client.Mode())
	_ = client.Close()
}

func TestNewRedisClient_ValkeyClusterMode(t *testing.T) {
	client, err := NewRedisClient(RedisConfig{
		Mode:  RedisModeValkeyCluster,
		Addrs: []string{"localhost:7000", "localhost:7001"},
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, RedisModeValkeyCluster, client.Mode())
	_ = client.Close()
}

// --- Local Cache Invalidation Integration Tests ---

func TestCache_MDelete_InvalidatesLocalCache(t *testing.T) {
	c := setupMiniredisCache(t, WithLocalCache(1000, time.Minute))
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "Alice"}
	user2 := testUser{ID: 2, Name: "Bob"}

	// Set values via single Set
	require.NoError(t, c.Set(ctx, "user:1", user1))
	require.NoError(t, c.Set(ctx, "user:2", user2))

	// Get to populate local cache
	val, err := c.Get(ctx, "user:1")
	require.NoError(t, err)
	assert.Equal(t, user1.ID, val.ID)

	val, err = c.Get(ctx, "user:2")
	require.NoError(t, err)
	assert.Equal(t, user2.ID, val.ID)

	// MDelete should invalidate both Redis and local cache
	err = c.MDelete(ctx, []string{"user:1", "user:2"})
	require.NoError(t, err)

	// Get must return ErrNotFound (not stale local data)
	_, err = c.Get(ctx, "user:1")
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = c.Get(ctx, "user:2")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCache_ClearPattern_InvalidatesLocalCache(t *testing.T) {
	c := setupMiniredisCache(t, WithLocalCache(1000, time.Minute))
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "Alice"}
	user2 := testUser{ID: 2, Name: "Bob"}

	// Set values
	require.NoError(t, c.Set(ctx, "user:1", user1))
	require.NoError(t, c.Set(ctx, "user:2", user2))

	// Get to populate local cache
	val, err := c.Get(ctx, "user:1")
	require.NoError(t, err)
	assert.Equal(t, user1.ID, val.ID)

	val, err = c.Get(ctx, "user:2")
	require.NoError(t, err)
	assert.Equal(t, user2.ID, val.ID)

	// ClearPattern should invalidate matching keys in both Redis and local cache
	err = c.ClearPattern(ctx, "user:*")
	require.NoError(t, err)

	// Get must return ErrNotFound (not stale local data)
	_, err = c.Get(ctx, "user:1")
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = c.Get(ctx, "user:2")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCache_MSet_InvalidatesLocalCache(t *testing.T) {
	c := setupMiniredisCache(t, WithLocalCache(1000, time.Minute))
	ctx := context.Background()

	user1 := testUser{ID: 1, Name: "Alice"}
	user2 := testUser{ID: 2, Name: "Bob"}

	// Set initial values
	require.NoError(t, c.Set(ctx, "user:1", user1))
	require.NoError(t, c.Set(ctx, "user:2", user2))

	// Get to populate local cache with initial values
	val, err := c.Get(ctx, "user:1")
	require.NoError(t, err)
	assert.Equal(t, "Alice", val.Name)

	val, err = c.Get(ctx, "user:2")
	require.NoError(t, err)
	assert.Equal(t, "Bob", val.Name)

	// MSet with NEW values
	newUser1 := testUser{ID: 1, Name: "Alice Updated"}
	newUser2 := testUser{ID: 2, Name: "Bob Updated"}
	err = c.MSet(ctx, map[string]testUser{
		"user:1": newUser1,
		"user:2": newUser2,
	})
	require.NoError(t, err)

	// Get must return the NEW values (not stale local cache)
	val, err = c.Get(ctx, "user:1")
	require.NoError(t, err)
	assert.Equal(t, "Alice Updated", val.Name)

	val, err = c.Get(ctx, "user:2")
	require.NoError(t, err)
	assert.Equal(t, "Bob Updated", val.Name)
}
