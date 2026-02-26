package supercache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisMode represents the Redis deployment mode.
type RedisMode string

const (
	// RedisModeStandalone is for single Redis instance.
	RedisModeStandalone RedisMode = "standalone"
	// RedisModeCluster is for Redis Cluster.
	RedisModeCluster RedisMode = "cluster"
	// RedisModeFailover is for Redis Sentinel (failover) mode.
	RedisModeFailover RedisMode = "failover"
	// RedisModeValkey is for Valkey standalone (wire-compatible with Redis).
	RedisModeValkey RedisMode = "valkey"
	// RedisModeValkeyCluster is for Valkey Cluster.
	RedisModeValkeyCluster RedisMode = "valkey-cluster"
)

// RedisConfig holds Redis connection configuration.
type RedisConfig struct {
	// Mode specifies standalone or cluster mode.
	Mode RedisMode
	// Addrs is the list of Redis addresses.
	// For standalone: single address (e.g., "localhost:6379")
	// For cluster: multiple addresses
	Addrs []string
	// Password for Redis authentication.
	Password string
	// MasterName is the name of the Sentinel master (required for failover mode).
	MasterName string
	// SentinelPassword is the password for Sentinel authentication (optional).
	SentinelPassword string
	// DB selects the database (standalone mode only, ignored in cluster).
	DB int
	// UseTLS enables TLS connection.
	UseTLS bool
	// PoolSize is the maximum number of connections.
	PoolSize int
	// MinIdleConns is the minimum number of idle connections.
	MinIdleConns int
	// DialTimeout is the timeout for establishing new connections.
	DialTimeout time.Duration
	// ReadTimeout is the timeout for read operations.
	ReadTimeout time.Duration
	// WriteTimeout is the timeout for write operations.
	WriteTimeout time.Duration
}

// DefaultRedisConfig returns a RedisConfig with sensible defaults.
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		Mode:         RedisModeStandalone,
		Addrs:        []string{"localhost:6379"},
		PoolSize:     10,
		MinIdleConns: 2,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
}

// scanner is an interface for Redis SCAN operation.
type scanner interface {
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
}

// RedisClient wraps Redis client for both standalone and cluster modes.
type RedisClient struct {
	cmdable redis.Cmdable
	scanner scanner
	mode    RedisMode
	closer  func() error
}

// NewRedisClient creates a new Redis client based on config.
func NewRedisClient(cfg RedisConfig) (*RedisClient, error) {
	if len(cfg.Addrs) == 0 {
		return nil, fmt.Errorf("%w: no addresses provided", ErrInvalidConfig)
	}

	var tlsConfig *tls.Config
	if cfg.UseTLS {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	var cmdable redis.Cmdable
	var closer func() error

	var sc scanner

	switch cfg.Mode {
	case RedisModeCluster, RedisModeValkeyCluster:
		client := redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        cfg.Addrs,
			Password:     cfg.Password,
			PoolSize:     cfg.PoolSize,
			MinIdleConns: cfg.MinIdleConns,
			DialTimeout:  cfg.DialTimeout,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			TLSConfig:    tlsConfig,
		})
		cmdable = client
		sc = client
		closer = client.Close

	case RedisModeFailover:
		if cfg.MasterName == "" {
			return nil, fmt.Errorf("%w: MasterName is required for failover mode", ErrInvalidConfig)
		}
		client := redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       cfg.MasterName,
			SentinelAddrs:    cfg.Addrs,
			SentinelPassword: cfg.SentinelPassword,
			Password:         cfg.Password,
			DB:               cfg.DB,
			PoolSize:         cfg.PoolSize,
			MinIdleConns:     cfg.MinIdleConns,
			DialTimeout:      cfg.DialTimeout,
			ReadTimeout:      cfg.ReadTimeout,
			WriteTimeout:     cfg.WriteTimeout,
			TLSConfig:        tlsConfig,
		})
		cmdable = client
		sc = client
		closer = client.Close

	case RedisModeStandalone, RedisModeValkey:
		fallthrough
	default:
		client := redis.NewClient(&redis.Options{
			Addr:         cfg.Addrs[0],
			Password:     cfg.Password,
			DB:           cfg.DB,
			PoolSize:     cfg.PoolSize,
			MinIdleConns: cfg.MinIdleConns,
			DialTimeout:  cfg.DialTimeout,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			TLSConfig:    tlsConfig,
		})
		cmdable = client
		sc = client
		closer = client.Close
	}

	return &RedisClient{
		cmdable: cmdable,
		scanner: sc,
		mode:    cfg.Mode,
		closer:  closer,
	}, nil
}

// NewRedisClientFromCmdable creates a RedisClient from an existing redis.Cmdable.
// Useful for testing or when you already have a Redis client.
// If cmdable also implements the scanner interface, it will be used for SCAN operations.
func NewRedisClientFromCmdable(cmdable redis.Cmdable) *RedisClient {
	rc := &RedisClient{
		cmdable: cmdable,
		mode:    RedisModeStandalone,
		closer:  func() error { return nil },
	}
	if sc, ok := cmdable.(scanner); ok {
		rc.scanner = sc
	}
	return rc
}

// Cmdable returns the underlying redis.Cmdable interface.
func (r *RedisClient) Cmdable() redis.Cmdable {
	return r.cmdable
}

// Mode returns the Redis deployment mode.
func (r *RedisClient) Mode() RedisMode {
	return r.mode
}

// Ping checks the Redis connection.
func (r *RedisClient) Ping(ctx context.Context) error {
	if err := r.cmdable.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrConnection, err)
	}
	return nil
}

// Scan performs a Redis SCAN operation.
func (r *RedisClient) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	if r.scanner != nil {
		return r.scanner.Scan(ctx, cursor, match, count).Result()
	}
	return nil, 0, errors.New("supercache: redis client does not support SCAN")
}

// Close closes the Redis connection.
func (r *RedisClient) Close() error {
	if r.closer != nil {
		return r.closer()
	}
	return nil
}
