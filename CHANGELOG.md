# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- **Batch operations now invalidate local cache (TinyLFU)** — `MDelete`, `MSet`, `Clear`, and `ClearPattern` previously only operated on Redis, leaving stale entries in the local TinyLFU cache. Now all batch operations correctly call `DeleteFromLocalCache` after the Redis operation succeeds, ensuring local cache consistency.

### Added

- Integration tests for local cache invalidation in batch operations (`MDelete`, `ClearPattern`, `MSet`).

## [0.1.0]

### Added

- `Cache[T any]` generic interface with type-safe operations.
- Dual-layer caching: local TinyLFU + Redis with singleflight deduplication.
- Core operations: `Get`, `Set`, `SetWithTTL`, `Delete`, `Exists`, `GetTTL`.
- `GetOrSet` / `GetOrSetPtr` load-through cache pattern.
- Null value caching (`SetNull`, `GetOrSetPtr`) to prevent cache penetration.
- Batch operations: `MGet`, `MSet`, `MSetWithTTL`, `MDelete`.
- `Clear` and `ClearPattern` for bulk key deletion via SCAN.
- `Refresh` for TTL renewal without value change.
- `Close` for graceful shutdown with `ErrClosed` guard.
- Circuit breaker (Closed / Open / Half-Open) with configurable threshold and cooldown.
- Graceful degradation via `WithFallbackOnError`.
- TTL jitter to prevent cache avalanche.
- Observability hooks: `OnHit`, `OnMiss`, `OnError`, `OnSet`, `OnDelete`.
- Hook panic recovery via `safeHooks` with `slog` logging.
- Functional options: `WithKeyPrefix`, `WithDefaultTTL`, `WithNullTTL`, `WithJitterPercent`, `WithLocalCache`, `WithoutLocalCache`, `WithSerializer`, `WithHooks`, `WithFallbackOnError`, `WithCircuitBreaker`, `WithScanBatchSize`, `WithMaxBatchSize`, `WithMaxKeyLength`, `WithMaxValueSize`.
- Serializers: MsgPack (default) and JSON.
- Redis modes: Standalone, Cluster, Sentinel (Failover), Valkey, Valkey Cluster.
- TLS support for Redis connections.
- Key validation with configurable max length.
- Value size validation with configurable max size.
- Batch size limits with configurable max batch size.
- Sentinel errors: `ErrNotFound`, `ErrNullValue`, `ErrClosed`, `ErrCircuitOpen`, `ErrInvalidConfig`, `ErrConnection`, `ErrSerialization`, `ErrBatchTooLarge`.
