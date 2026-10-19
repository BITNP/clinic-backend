package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisRecordActionStore implements RecordActionStore on top of a
// Redis sorted set per staff member. Members are JSON-encoded actions scored by
// creation time; reads lazily trim entries that fell out of the window.
type redisRecordActionStore struct {
	client *redis.Client
}

// NewRedisRecordActionStore creates the Redis-backed per-operator undo journal.
func NewRedisRecordActionStore(client *redis.Client) *redisRecordActionStore {
	return &redisRecordActionStore{client: client}
}

func recordActionKey(actorID int) string {
	return "clinic:record:actions:" + strconv.Itoa(actorID)
}

func (s *redisRecordActionStore) Push(ctx context.Context, actorID int, entry RecordActionEntry, ttl time.Duration) error {
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

func (s *redisRecordActionStore) Window(ctx context.Context, actorID int, cutoff time.Time) ([]RecordActionEntry, error) {
	key := recordActionKey(actorID)
	if err := s.client.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(cutoff.UnixMilli(), 10)).Err(); err != nil {
		return nil, fmt.Errorf("prune record actions: %w", err)
	}
	members, err := s.client.ZRevRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("read record actions: %w", err)
	}
	out := make([]RecordActionEntry, 0, len(members))
	for _, member := range members {
		var entry RecordActionEntry
		if err := json.Unmarshal([]byte(member), &entry); err != nil {
			log.Printf("redis: skip corrupt record action: %v", err)
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *redisRecordActionStore) Remove(ctx context.Context, actorID int, entry RecordActionEntry) error {
	member, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal record action: %w", err)
	}
	if err := s.client.ZRem(ctx, recordActionKey(actorID), member).Err(); err != nil {
		return fmt.Errorf("remove record action: %w", err)
	}
	return nil
}
