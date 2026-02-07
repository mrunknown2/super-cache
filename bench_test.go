package supercache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupBenchCache(b *testing.B) Cache[testUser] {
	mr := miniredis.RunT(b)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	b.Cleanup(func() { _ = rdb.Close() })

	redisClient := NewRedisClientFromCmdable(rdb)
	c, err := New[testUser](redisClient,
		WithKeyPrefix("bench:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithoutLocalCache(),
	)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func setupBenchCacheWithLocal(b *testing.B) Cache[testUser] {
	mr := miniredis.RunT(b)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	b.Cleanup(func() { _ = rdb.Close() })

	redisClient := NewRedisClientFromCmdable(rdb)
	c, err := New[testUser](redisClient,
		WithKeyPrefix("bench:"),
		WithDefaultTTL(5*time.Minute),
		WithJitterPercent(0),
		WithLocalCache(1000, time.Minute),
	)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func BenchmarkSet(b *testing.B) {
	c := setupBenchCache(b)
	ctx := context.Background()
	user := testUser{ID: 1, Name: "John"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("user:%d", i)
		_ = c.Set(ctx, key, user)
	}
}

func BenchmarkGet_Hit(b *testing.B) {
	c := setupBenchCache(b)
	ctx := context.Background()
	user := testUser{ID: 1, Name: "John"}
	_ = c.Set(ctx, "user:1", user)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get(ctx, "user:1")
	}
}

func BenchmarkGet_Miss(b *testing.B) {
	c := setupBenchCache(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get(ctx, "user:nonexistent")
	}
}

func BenchmarkGetOrSet(b *testing.B) {
	c := setupBenchCache(b)
	ctx := context.Background()
	user := testUser{ID: 1, Name: "John"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.GetOrSet(ctx, "user:getorset", func() (testUser, error) {
			return user, nil
		})
	}
}

func BenchmarkMGet(b *testing.B) {
	c := setupBenchCache(b)
	ctx := context.Background()

	// Pre-populate
	for i := 0; i < 10; i++ {
		_ = c.Set(ctx, fmt.Sprintf("user:%d", i), testUser{ID: int64(i), Name: fmt.Sprintf("User%d", i)})
	}

	keys := make([]string, 10)
	for i := 0; i < 10; i++ {
		keys[i] = fmt.Sprintf("user:%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.MGet(ctx, keys)
	}
}

func BenchmarkMSet(b *testing.B) {
	c := setupBenchCache(b)
	ctx := context.Background()

	items := make(map[string]testUser, 10)
	for i := 0; i < 10; i++ {
		items[fmt.Sprintf("user:%d", i)] = testUser{ID: int64(i), Name: fmt.Sprintf("User%d", i)}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.MSet(ctx, items)
	}
}

func BenchmarkGet_LocalCacheHit(b *testing.B) {
	c := setupBenchCacheWithLocal(b)
	ctx := context.Background()
	user := testUser{ID: 1, Name: "John"}
	_ = c.Set(ctx, "user:1", user)

	// Warm local cache
	_, _ = c.Get(ctx, "user:1")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get(ctx, "user:1")
	}
}
