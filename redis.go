package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"clinic-backend/services"

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

// redisRecordActionStore implements services.RecordActionStore on top of a
// Redis sorted set per staff member. Members are JSON-encoded actions scored by
// creation time; reads lazily trim entries that fell out of the window.
type redisRecordActionStore struct {
	client *redis.Client
}

func newRedisRecordActionStore(client *redis.Client) *redisRecordActionStore {
	return &redisRecordActionStore{client: client}
}

func recordActionKey(actorID int) string {
	return "clinic:record:actions:" + strconv.Itoa(actorID)
}

func (s *redisRecordActionStore) Push(ctx context.Context, actorID int, entry services.RecordActionEntry, ttl time.Duration) error {
	member, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal record action: %w", err)
	}
	key := recordActionKey(actorID)
	pipe := s.client.TxPipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(entry.CreatedAt.UnixMilli()), Member: member})
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("push record action: %w", err)
	}
	return nil
}

func (s *redisRecordActionStore) Window(ctx context.Context, actorID int, cutoff time.Time) ([]services.RecordActionEntry, error) {
	key := recordActionKey(actorID)
	if err := s.client.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(cutoff.UnixMilli(), 10)).Err(); err != nil {
		return nil, fmt.Errorf("prune record actions: %w", err)
	}
	members, err := s.client.ZRevRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("read record actions: %w", err)
	}
	out := make([]services.RecordActionEntry, 0, len(members))
	for _, member := range members {
		var entry services.RecordActionEntry
		if err := json.Unmarshal([]byte(member), &entry); err != nil {
			log.Printf("redis: skip corrupt record action: %v", err)
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *redisRecordActionStore) Remove(ctx context.Context, actorID int, entry services.RecordActionEntry) error {
	member, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal record action: %w", err)
	}
	if err := s.client.ZRem(ctx, recordActionKey(actorID), member).Err(); err != nil {
		return fmt.Errorf("remove record action: %w", err)
	}
	return nil
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
