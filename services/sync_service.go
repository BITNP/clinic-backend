package services

import (
	"context"
	"fmt"
)

// SyncGroup identifies a collection of data whose changes clients poll for.
// Each group has its own monotonically increasing counter.
type SyncGroup string

const (
	SyncGroupRecord       SyncGroup = "record"
	SyncGroupRoom         SyncGroup = "room"
	SyncGroupWorkSchedule SyncGroup = "work_schedule"
	SyncGroupServiceDate  SyncGroup = "service_date"
	SyncGroupStaff        SyncGroup = "staff"
	SyncGroupAnnouncement SyncGroup = "announcement"
)

// allSyncGroups lists every group reported by Snapshot. Keep this in one place
// so the counter key and the API response never drift apart.
var allSyncGroups = []SyncGroup{
	SyncGroupRecord,
	SyncGroupRoom,
	SyncGroupWorkSchedule,
	SyncGroupServiceDate,
	SyncGroupStaff,
	SyncGroupAnnouncement,
}

// syncKeyPrefix namespaces the counters inside Redis.
const syncKeyPrefix = "clinic:sync:"

// SyncStore is the counter backend. The Redis implementation lives in the web
// layer (main package) so this package stays free of Redis details, and tests
// can substitute an in-memory fake.
type SyncStore interface {
	// Incr increments the counter for key and returns the new value.
	Incr(ctx context.Context, key string) (int64, error)
	// Get returns the counters for keys. Keys that were never set must be
	// reported as 0.
	Get(ctx context.Context, keys []string) (map[string]int64, error)
}

// SyncService maintains per-group change counters. Clients keep the counter
// values they last saw and compare them with a fresh Snapshot to decide when to
// refresh cached data.
type SyncService struct {
	store SyncStore
}

func NewSyncService(store SyncStore) *SyncService {
	return &SyncService{store: store}
}

func syncKey(g SyncGroup) string {
	return syncKeyPrefix + string(g)
}

// Bump increments the counter for g. It is best-effort: callers should log, not
// fail, an error here so a counter backend outage never fails a data write.
func (s *SyncService) Bump(ctx context.Context, g SyncGroup) error {
	if s == nil || s.store == nil {
		return nil
	}
	if _, err := s.store.Incr(ctx, syncKey(g)); err != nil {
		return fmt.Errorf("bump sync counter %s: %w", g, err)
	}
	return nil
}

// Snapshot returns the current counter for every group. Groups that were never
// bumped are reported as 0.
func (s *SyncService) Snapshot(ctx context.Context) (map[SyncGroup]int64, error) {
	out := make(map[SyncGroup]int64, len(allSyncGroups))
	if s == nil || s.store == nil {
		return out, nil
	}

	keys := make([]string, len(allSyncGroups))
	for i, g := range allSyncGroups {
		keys[i] = syncKey(g)
	}

	values, err := s.store.Get(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("read sync counters: %w", err)
	}
	for _, g := range allSyncGroups {
		out[g] = values[syncKey(g)]
	}
	return out, nil
}
