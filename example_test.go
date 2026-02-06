package supercache_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mrunknown2/super-cache"
)

func mustSetup() (supercache.Cache[string], *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	rc, err := supercache.NewRedisClient(supercache.RedisConfig{
		Mode:  supercache.RedisModeStandalone,
		Addrs: []string{mr.Addr()},
	})
	if err != nil {
		panic(err)
	}
	c, err := supercache.New[string](rc,
		supercache.WithKeyPrefix("ex:"),
		supercache.WithDefaultTTL(5*time.Minute),
		supercache.WithoutLocalCache(),
	)
	if err != nil {
		panic(err)
	}
	return c, mr
}

func ExampleNew() {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	defer mr.Close()

	rc, err := supercache.NewRedisClient(supercache.RedisConfig{
		Mode:  supercache.RedisModeStandalone,
		Addrs: []string{mr.Addr()},
	})
	if err != nil {
		panic(err)
	}

	type User struct {
		ID   int
		Name string
	}

	cache, err := supercache.New[User](rc,
		supercache.WithKeyPrefix("user:"),
		supercache.WithDefaultTTL(10*time.Minute),
		supercache.WithLocalCache(1000, time.Minute),
	)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	_ = cache.Set(ctx, "1", User{ID: 1, Name: "Alice"})

	user, err := cache.Get(ctx, "1")
	if err != nil {
		panic(err)
	}
	fmt.Println(user.Name)
	// Output: Alice
}

func ExampleCache_Get() {
	c, mr := mustSetup()
	defer mr.Close()
	ctx := context.Background()

	_ = c.Set(ctx, "greeting", "hello")

	val, err := c.Get(ctx, "greeting")
	if err != nil {
		panic(err)
	}
	fmt.Println(val)
	// Output: hello
}

func ExampleCache_GetOrSet() {
	c, mr := mustSetup()
	defer mr.Close()
	ctx := context.Background()

	// First call: cache miss, fn is called
	val, err := c.GetOrSet(ctx, "data", func() (string, error) {
		return "loaded from db", nil
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(val)

	// Second call: cache hit, fn is NOT called
	val, err = c.GetOrSet(ctx, "data", func() (string, error) {
		return "should not see this", nil
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(val)
	// Output:
	// loaded from db
	// loaded from db
}

func ExampleCache_GetOrSetPtr() {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	defer mr.Close()

	rc, err := supercache.NewRedisClient(supercache.RedisConfig{
		Mode:  supercache.RedisModeStandalone,
		Addrs: []string{mr.Addr()},
	})
	if err != nil {
		panic(err)
	}

	type Product struct {
		ID   int
		Name string
	}

	cache, err := supercache.New[Product](rc,
		supercache.WithKeyPrefix("product:"),
		supercache.WithoutLocalCache(),
	)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	// When fn returns nil, a null entry is cached to prevent cache penetration
	result, err := cache.GetOrSetPtr(ctx, "999", func() (*Product, error) {
		// Simulate: product not found in database
		return nil, nil
	})
	if errors.Is(err, supercache.ErrNullValue) {
		fmt.Println("product not found (null cached)")
	}
	fmt.Println(result)
	// Output:
	// product not found (null cached)
	// <nil>
}

func ExampleCache_MGet() {
	c, mr := mustSetup()
	defer mr.Close()
	ctx := context.Background()

	_ = c.Set(ctx, "a", "alpha")
	_ = c.Set(ctx, "b", "bravo")

	results, err := c.MGet(ctx, []string{"a", "b", "c"})
	if err != nil {
		panic(err)
	}
	fmt.Println(results["a"])
	fmt.Println(results["b"])
	_, exists := results["c"]
	fmt.Println("c exists:", exists)
	// Output:
	// alpha
	// bravo
	// c exists: false
}

func ExampleWithCircuitBreaker() {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	defer mr.Close()

	rc, err := supercache.NewRedisClient(supercache.RedisConfig{
		Mode:  supercache.RedisModeStandalone,
		Addrs: []string{mr.Addr()},
	})
	if err != nil {
		panic(err)
	}

	_, err = supercache.New[string](rc,
		supercache.WithCircuitBreaker(supercache.CircuitBreakerConfig{
			FailureThreshold: 5,
			Cooldown:         10 * time.Second,
		}),
		supercache.WithFallbackOnError(true),
		supercache.WithoutLocalCache(),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("cache with circuit breaker created")
	// Output: cache with circuit breaker created
}
