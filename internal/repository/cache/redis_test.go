package cache

import (
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestNewRedisCache(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	rc := NewRedisCache(client)
	assert.NotNil(t, rc)
}

// TestRedisCacheImplementsCache verifies that *RedisCache satisfies the Cache interface.
func TestRedisCacheImplementsCache(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	var _ Cache = NewRedisCache(client)
}
