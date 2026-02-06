package supercache

import (
	"context"
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
