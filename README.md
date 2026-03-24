# supercache

Type-safe, generic Redis cache library for Go with dual-layer caching, circuit breaker, and null value caching.

ไลบรารี Redis cache สำหรับ Go ที่รองรับ generics, type-safe, พร้อม dual-layer caching, circuit breaker, และ null value caching

---

## Features / ฟีเจอร์

- **Type-safe Generics** — `Cache[T any]` interface, no type assertions needed / ไม่ต้อง cast type เอง
- **Dual-layer Cache** — Local TinyLFU + Redis with singleflight deduplication / cache สองชั้น ลด round-trip ไป Redis
- **Null Value Caching** — Prevents cache penetration by caching nil results / ป้องกัน cache penetration โดย cache ค่า nil
- **Circuit Breaker** — Auto-trips after consecutive Redis failures / ตัดวงจรอัตโนมัติเมื่อ Redis ล่มติดต่อกัน
- **Graceful Degradation** — Falls back to direct calls when Redis is down / degradation อัตโนมัติเมื่อ Redis มีปัญหา
- **TTL Jitter** — Randomizes TTL to prevent cache avalanche / สุ่ม TTL เพื่อป้องกัน cache avalanche
- **Observability Hooks** — OnHit, OnMiss, OnError, OnSet, OnDelete callbacks
- **Batch Operations** — MGet/MSet/MDelete with pipeline, full local cache sync / รองรับ batch operation ผ่าน pipeline พร้อม sync local cache
- **Hook Panic Recovery** — Hooks that panic are recovered and logged via `slog` / Hook ที่ panic จะถูก recover และ log ผ่าน `slog`
- **Lifecycle Management** — `Close()` for graceful shutdown, `Refresh()` for TTL renewal / จัดการ lifecycle ด้วย `Close()` และ `Refresh()`
- **Standalone, Cluster, Sentinel & Valkey** — Supports Redis standalone, cluster, Sentinel failover, and Valkey / รองรับ Redis standalone, cluster, Sentinel failover, และ Valkey

## Installation / การติดตั้ง

```bash
go get github.com/mrunknown2/super-cache@v0.1.0
```

Requires Go 1.22+ (generics support).

## Quick Start / เริ่มต้นใช้งาน

```go
package main

import (
	"context"
	"fmt"
	"time"

	supercache "github.com/mrunknown2/super-cache"
)

type User struct {
	ID   int
	Name string
}

func main() {
	// 1. Create Redis client / สร้าง Redis client
	rc, err := supercache.NewRedisClient(supercache.RedisConfig{
		Mode:  supercache.RedisModeStandalone,
		Addrs: []string{"localhost:6379"},
	})
	if err != nil {
		panic(err)
	}

	// 2. Create type-safe cache / สร้าง cache แบบ type-safe
	cache, err := supercache.New[User](rc,
		supercache.WithKeyPrefix("user:"),
		supercache.WithDefaultTTL(10*time.Minute),
		supercache.WithLocalCache(1000, time.Minute),
	)
	if err != nil {
		panic(err)
	}
	defer cache.Close() // always close when done / ปิดเมื่อใช้งานเสร็จ

	ctx := context.Background()

	// 3. Set & Get
	_ = cache.Set(ctx, "1", User{ID: 1, Name: "Alice"})

	user, err := cache.Get(ctx, "1")
	if err != nil {
		panic(err)
	}
	fmt.Println(user.Name) // Alice
}
```

## API

### Creating a Cache / สร้าง Cache

```go
cache, err := supercache.New[T](redisClient, opts ...Option)
```

### Core Operations / การใช้งานหลัก

| Method | Description / คำอธิบาย |
|--------|------------------------|
| `Get(ctx, key)` | Get value by key / ดึงค่าจาก key |
| `Set(ctx, key, value)` | Set value with default TTL / เก็บค่าด้วย TTL เริ่มต้น |
| `SetWithTTL(ctx, key, value, ttl)` | Set value with custom TTL / เก็บค่าด้วย TTL ที่กำหนดเอง |
| `SetNull(ctx, key)` | Cache a null marker (penetration prevention) / cache ค่า null เพื่อป้องกัน penetration |
| `Delete(ctx, key)` | Delete a key / ลบ key |
| `MDelete(ctx, keys)` | Delete multiple keys / ลบหลาย key พร้อมกัน |
| `Exists(ctx, key)` | Check if key exists / ตรวจสอบว่า key มีอยู่หรือไม่ |
| `GetTTL(ctx, key)` | Get remaining TTL / ดู TTL ที่เหลือของ key |
| `Refresh(ctx, key, ttl)` | Update TTL without changing value / อัปเดต TTL โดยไม่เปลี่ยนค่า |
| `Close()` | Shut down cache, release resources / ปิด cache และคืน resource |
| `CircuitBreakerState()` | Get circuit breaker state / ดูสถานะ circuit breaker |

### GetOrSet Pattern

Load-through cache: get from cache, or call the function and cache the result.

ดึงจาก cache ถ้าไม่เจอจะเรียก function แล้ว cache ผลลัพธ์ให้อัตโนมัติ

```go
user, err := cache.GetOrSet(ctx, "user:1", func() (User, error) {
    return db.FindUser(1) // called only on cache miss
})
```

### Null Value Caching / การ Cache ค่า Null

`GetOrSetPtr` caches nil results to prevent repeated database lookups for non-existent records (cache penetration).

ป้องกัน cache penetration โดย cache ค่า nil เมื่อ record ไม่มีอยู่ในฐานข้อมูล

```go
product, err := cache.GetOrSetPtr(ctx, "product:999", func() (*Product, error) {
    return db.FindProduct(999) // returns nil if not found
})
if errors.Is(err, supercache.ErrNullValue) {
    // product does not exist, but this result is cached
    // ไม่มี product นี้ แต่ผลลัพธ์ถูก cache ไว้แล้ว
}
```

### Batch Operations / การดำเนินการแบบกลุ่ม

```go
// MSet — set multiple values via pipeline
_ = cache.MSet(ctx, map[string]string{
    "a": "alpha",
    "b": "bravo",
})

// MGet — get multiple values
results, _ := cache.MGet(ctx, []string{"a", "b", "c"})
// results["a"] = "alpha", results["b"] = "bravo", "c" not present

// MDelete — delete multiple keys
_ = cache.MDelete(ctx, []string{"a", "b"})
```

### Refresh TTL

Extend a key's TTL without fetching or modifying its value.

ต่ออายุ TTL ของ key โดยไม่ต้องดึงหรือแก้ไขค่า

```go
err := cache.Refresh(ctx, "user:1", 10*time.Minute)
if errors.Is(err, supercache.ErrNotFound) {
    // key does not exist / key ไม่มีอยู่
}
```

### Lifecycle / การจัดการ Lifecycle

Always close the cache when your application shuts down to release Redis connections.

ปิด cache เสมอเมื่อ application shutdown เพื่อคืน connection

```go
cache, _ := supercache.New[User](rc, ...)
defer cache.Close()

// After Close(), all operations return ErrClosed
// หลังเรียก Close() ทุก operation จะ return ErrClosed
```

### Clear / ล้าง Cache

```go
cache.Clear(ctx)                    // delete all keys with this prefix / ลบทุก key ที่มี prefix นี้
cache.ClearPattern(ctx, "user:*")   // delete keys matching pattern / ลบ key ที่ตรงกับ pattern
```

## Options / ตัวเลือกการตั้งค่า

Configure via functional options when calling `New[T]()`:

ตั้งค่าผ่าน functional options ตอนสร้าง cache

| Option | Default | Description / คำอธิบาย |
|--------|---------|------------------------|
| `WithKeyPrefix(prefix)` | `"sc:"` | Key prefix / prefix ของ key |
| `WithDefaultTTL(ttl)` | `5m` | Default TTL / TTL เริ่มต้น |
| `WithNullTTL(ttl)` | `30s` | TTL for null entries / TTL สำหรับค่า null |
| `WithJitterPercent(pct)` | `0.1` | TTL jitter 0.0-1.0 / เปอร์เซ็นต์การสุ่ม TTL |
| `WithLocalCache(size, ttl)` | `1000, 1m` | Local TinyLFU cache / cache ในหน่วยความจำ |
| `WithoutLocalCache()` | — | Disable local cache / ปิด local cache |
| `WithSerializer(s)` | MsgPack | Serializer (MsgPack or JSON) |
| `WithHooks(h)` | NoopHooks | Observability hooks |
| `WithFallbackOnError(true)` | `false` | Graceful degradation / degradation อัตโนมัติ |
| `WithCircuitBreaker(cfg)` | disabled | Circuit breaker config |
| `WithScanBatchSize(n)` | `100` | Keys per SCAN iteration in Clear/ClearPattern |

## Circuit Breaker

Protects your application when Redis is unavailable. After consecutive failures exceed the threshold, the circuit opens and skips Redis entirely until the cooldown expires.

ป้องกัน application เมื่อ Redis ล่ม เมื่อ error ติดต่อกันเกิน threshold จะหยุดเรียก Redis จนกว่าจะครบ cooldown

```go
cache, _ := supercache.New[string](rc,
    supercache.WithCircuitBreaker(supercache.CircuitBreakerConfig{
        FailureThreshold: 5,               // open after 5 consecutive failures
        Cooldown:         10 * time.Second, // wait 10s before retrying
    }),
    supercache.WithFallbackOnError(true),   // degrade gracefully
)
```

**States / สถานะ:** Closed (normal) → Open (rejecting) → Half-Open (probing)

Query the current state programmatically / ดูสถานะปัจจุบันได้ผ่านโค้ด:

```go
state := cache.CircuitBreakerState()
// supercache.CircuitClosed, supercache.CircuitOpen, or supercache.CircuitHalfOpen
fmt.Println(state) // "closed", "open", or "half-open"
```

## Observability Hooks

Implement the `Hooks` interface for monitoring, logging, or metrics.

implement interface `Hooks` สำหรับ monitoring, logging, หรือ metrics

```go
type Hooks interface {
    OnHit(ctx context.Context, key string)
    OnMiss(ctx context.Context, key string)
    OnError(ctx context.Context, key string, err error)
    OnSet(ctx context.Context, key string, ttl time.Duration)
    OnDelete(ctx context.Context, key string)
}
```

```go
cache, _ := supercache.New[User](rc,
    supercache.WithHooks(myPrometheusHooks{}),
)
```

## Redis Configuration / การตั้งค่า Redis

### Standalone

```go
rc, _ := supercache.NewRedisClient(supercache.RedisConfig{
    Mode:     supercache.RedisModeStandalone,
    Addrs:    []string{"localhost:6379"},
    Password: "secret",
    DB:       0,
})
```

### Cluster

```go
rc, _ := supercache.NewRedisClient(supercache.RedisConfig{
    Mode:     supercache.RedisModeCluster,
    Addrs:    []string{"node1:6379", "node2:6379", "node3:6379"},
    Password: "secret",
    UseTLS:   true,
})
```

### Sentinel (Failover)

```go
rc, _ := supercache.NewRedisClient(supercache.RedisConfig{
    Mode:             supercache.RedisModeFailover,
    Addrs:            []string{"sentinel1:26379", "sentinel2:26379", "sentinel3:26379"},
    MasterName:       "mymaster",
    Password:         "secret",          // master password
    SentinelPassword: "sentinel-secret", // optional sentinel password
})
```

### Valkey

[Valkey](https://valkey.io/) is wire-compatible with Redis. Use `RedisModeValkey` or `RedisModeValkeyCluster`.

Valkey เข้ากันได้กับ Redis protocol ใช้ mode `RedisModeValkey` หรือ `RedisModeValkeyCluster`

```go
// Valkey standalone
rc, _ := supercache.NewRedisClient(supercache.RedisConfig{
    Mode:  supercache.RedisModeValkey,
    Addrs: []string{"localhost:6379"},
})

// Valkey cluster
rc, _ := supercache.NewRedisClient(supercache.RedisConfig{
    Mode:  supercache.RedisModeValkeyCluster,
    Addrs: []string{"node1:6379", "node2:6379"},
})
```

## Errors / ข้อผิดพลาด

| Error | Description / คำอธิบาย |
|-------|------------------------|
| `ErrNotFound` | Key does not exist / ไม่พบ key |
| `ErrNullValue` | Cached value is explicitly null / ค่าที่ cache เป็น null |
| `ErrClosed` | Cache has been closed / cache ถูกปิดแล้ว |
| `ErrCircuitOpen` | Circuit breaker is open / circuit breaker เปิดอยู่ |
| `ErrInvalidConfig` | Invalid configuration / การตั้งค่าไม่ถูกต้อง |
| `ErrConnection` | Redis connection failure / เชื่อมต่อ Redis ไม่ได้ |
| `ErrSerialization` | Marshal/unmarshal failure / serialize/deserialize ล้มเหลว |

## Architecture / สถาปัตยกรรม

```
Cache[T] interface  (+Close, +Refresh)
  └─ cacheImpl[T]
       ├─ go-redis/cache.Cache    — codec + local TinyLFU + singleflight
       ├─ CircuitBreaker          — Closed → Open → Half-Open
       ├─ safeHooks               — panic recovery with slog logging
       └─ RedisClient             — standalone / cluster / sentinel / valkey
```

## Testing / การทดสอบ

```bash
go test ./...            # run all tests / รัน test ทั้งหมด
go test -race ./...      # with race detector / ตรวจ race condition
go test -cover ./...     # with coverage / ดู coverage
go test -bench ./...     # run benchmarks / รัน benchmark
```

Tests use [miniredis](https://github.com/alicebob/miniredis) (in-memory Redis) and [redismock](https://github.com/go-redis/redismock) — no real Redis instance required.

ไม่ต้องมี Redis จริงในการรัน test

Benchmarks are available for all core operations (Get, Set, GetOrSet, MGet, MSet, local cache hit).

มี benchmark สำหรับทุก operation หลัก

## License

Apache-2.0 — see [LICENSE](LICENSE) for details.
