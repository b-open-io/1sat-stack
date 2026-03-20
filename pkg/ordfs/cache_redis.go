package ordfs

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache implements Cache backed by Redis.
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisCache creates a Redis-backed cache. TTL is parsed from a duration string (e.g., "24h").
// Empty or "0" TTL means no expiration.
func NewRedisCache(url, ttl string) (*RedisCache, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	var dur time.Duration
	if ttl != "" && ttl != "0" {
		dur, err = time.ParseDuration(ttl)
		if err != nil {
			return nil, err
		}
	}

	return &RedisCache{client: client, ttl: dur}, nil
}

func (r *RedisCache) Get(_ context.Context, key string) ([]byte, error) {
	val, err := r.client.Get(context.Background(), key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return val, err
}

func (r *RedisCache) Set(_ context.Context, key string, value []byte) error {
	return r.client.Set(context.Background(), key, value, r.ttl).Err()
}

func (r *RedisCache) Delete(_ context.Context, key string) error {
	return r.client.Del(context.Background(), key).Err()
}

func (r *RedisCache) Close() error {
	return r.client.Close()
}
