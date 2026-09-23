package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// redisConfig holds the Redis connection settings read from the environment.
type redisConfig struct {
	addr     string
	password string
	db       int
}

// newRedisClient opens a Redis client and verifies the connection with PING.
// Redis is required infrastructure, so a failed ping is returned as an error
// for the caller to treat as a startup failure.
func newRedisClient(ctx context.Context, cfg redisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.addr,
		Password: cfg.password,
		DB:       cfg.db,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		if closeErr := client.Close(); closeErr != nil {
			log.Printf("redis: close after failed ping: %v", closeErr)
		}
		return nil, fmt.Errorf("ping redis at %s: %w", cfg.addr, err)
	}
	return client, nil
}

// redisSyncStore implements services.SyncStore on top of Redis. The sync
// counters are plain integer keys modified with INCR, which is atomic across
// backend replicas.
type redisSyncStore struct {
	client *redis.Client
}

func newRedisSyncStore(client *redis.Client) *redisSyncStore {
	return &redisSyncStore{client: client}
}

func (s *redisSyncStore) Incr(ctx context.Context, key string) (int64, error) {
	return s.client.Incr(ctx, key).Result()
}

func (s *redisSyncStore) Get(ctx context.Context, keys []string) (map[string]int64, error) {
	out := make(map[string]int64, len(keys))
	if len(keys) == 0 {
		return out, nil
	}

	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, key := range keys {
		if i >= len(values) {
			break
		}
		out[key] = redisInt64(values[i])
	}
	return out, nil
}

// redisInt64 converts a Redis bulk value into a counter. Missing keys come back
// as nil and are treated as 0.
func redisInt64(v any) int64 {
	switch n := v.(type) {
	case nil:
		return 0
	case int64:
		return n
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0
		}
		return i
	case []byte:
		i, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			return 0
		}
		return i
	default:
		return 0
	}
}

// redisHealthHandler reports whether Redis is reachable. It is registered on
// /healthz so the connection is exercised after startup as well.
func redisHealthHandler(client *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "redis": "down"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "redis": "up"})
	}
}
