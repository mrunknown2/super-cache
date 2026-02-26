package supercache

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Hooks provides observability callbacks for cache operations.
type Hooks interface {
	// OnHit is called when a cache hit occurs.
	OnHit(ctx context.Context, key string)
	// OnMiss is called when a cache miss occurs.
	OnMiss(ctx context.Context, key string)
	// OnError is called when an error occurs.
	OnError(ctx context.Context, key string, err error)
	// OnSet is called after a value is set in cache.
	OnSet(ctx context.Context, key string, ttl time.Duration)
	// OnDelete is called after a key is deleted.
	OnDelete(ctx context.Context, key string)
}

// NoopHooks is a no-operation implementation of Hooks.
type NoopHooks struct{}

func (NoopHooks) OnHit(ctx context.Context, key string)                    {}
func (NoopHooks) OnMiss(ctx context.Context, key string)                   {}
func (NoopHooks) OnError(ctx context.Context, key string, err error)       {}
func (NoopHooks) OnSet(ctx context.Context, key string, ttl time.Duration) {}
func (NoopHooks) OnDelete(ctx context.Context, key string)                 {}

var _ Hooks = NoopHooks{}

// safeHooks wraps a Hooks implementation to recover from panics.
type safeHooks struct {
	inner Hooks
}

func recoverHookPanic(method, key string) {
	if r := recover(); r != nil {
		slog.Error("supercache: hook panic recovered",
			"method", method, "key", key, "panic", fmt.Sprintf("%v", r))
	}
}

func (s safeHooks) OnHit(ctx context.Context, key string) {
	defer recoverHookPanic("OnHit", key)
	s.inner.OnHit(ctx, key)
}

func (s safeHooks) OnMiss(ctx context.Context, key string) {
	defer recoverHookPanic("OnMiss", key)
	s.inner.OnMiss(ctx, key)
}

func (s safeHooks) OnError(ctx context.Context, key string, err error) {
	defer recoverHookPanic("OnError", key)
	s.inner.OnError(ctx, key, err)
}

func (s safeHooks) OnSet(ctx context.Context, key string, ttl time.Duration) {
	defer recoverHookPanic("OnSet", key)
	s.inner.OnSet(ctx, key, ttl)
}

func (s safeHooks) OnDelete(ctx context.Context, key string) {
	defer recoverHookPanic("OnDelete", key)
	s.inner.OnDelete(ctx, key)
}
