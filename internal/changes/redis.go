package changes

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// redisSyncStore implements SyncStore on top of Redis. The sync
// counters are plain integer keys modified with INCR, which is atomic across
// backend replicas.
type redisSyncStore struct {
	client *redis.Client
}

// NewRedisStore creates shared change-counter storage backed by Redis.
func NewRedisStore(client *redis.Client) *redisSyncStore {
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
