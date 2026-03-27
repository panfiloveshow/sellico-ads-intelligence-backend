// Package cache provides Redis-based caching operations.
package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache defines the interface for cache operations.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	InvalidateByPrefix(ctx context.Context, prefix string) error
}

// RedisCache implements Cache using Redis.
// Cache misses are represented as (nil, nil) — callers should check for a nil
// return value to distinguish a miss from an empty value.
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache creates a new RedisCache backed by the given Redis client.
func NewRedisCache(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

// Get retrieves a value by key. Returns (nil, nil) on cache miss.
func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return val, nil
}

// Set stores a value with the given TTL.
func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Delete removes a single key.
func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

// scanBatchSize is the number of keys to request per SCAN iteration.
const scanBatchSize = 100

// InvalidateByPrefix deletes all keys matching the given prefix using SCAN
// (safe for production, unlike KEYS). Keys are deleted in batches.
func (c *RedisCache) InvalidateByPrefix(ctx context.Context, prefix string) error {
	var cursor uint64
	pattern := prefix + "*"

	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, scanBatchSize).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return nil
}
