package changes_test

import (
	"context"
	"sync"
	"testing"

	"clinic-backend/internal/changes"
)

// fakeSyncStore is an in-memory services.SyncStore so the sync tests do not
// need a running Redis.
type fakeSyncStore struct {
	mu       sync.Mutex
	counters map[string]int64
}

func newFakeSyncStore() *fakeSyncStore {
	return &fakeSyncStore{counters: map[string]int64{}}
}

func (f *fakeSyncStore) Incr(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters[key]++
	return f.counters[key], nil
}

func (f *fakeSyncStore) Get(_ context.Context, keys []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(keys))
	for _, key := range keys {
		out[key] = f.counters[key]
	}
	return out, nil
}

var testSyncGroups = []changes.SyncGroup{
	changes.SyncGroupRecord,
	changes.SyncGroupRoom,
	changes.SyncGroupWorkSchedule,
	changes.SyncGroupServiceDate,
	changes.SyncGroupStaff,
	changes.SyncGroupAnnouncement,
}

func TestSyncService_Snapshot_DefaultsToZero(t *testing.T) {
	svc := changes.NewSyncService(newFakeSyncStore())

	got, err := svc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	for _, g := range testSyncGroups {
		if got[g] != 0 {
			t.Errorf("expected group %s to start at 0, got %d", g, got[g])
		}
	}
}

func TestSyncService_Bump_IncrementsOnlyThatGroup(t *testing.T) {
	svc := changes.NewSyncService(newFakeSyncStore())
	ctx := context.Background()

	if err := svc.Bump(ctx, changes.SyncGroupRecord); err != nil {
		t.Fatalf("bump record: %v", err)
	}
	if err := svc.Bump(ctx, changes.SyncGroupRecord); err != nil {
		t.Fatalf("bump record: %v", err)
	}
	if err := svc.Bump(ctx, changes.SyncGroupRoom); err != nil {
		t.Fatalf("bump room: %v", err)
	}

	got, err := svc.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if got[changes.SyncGroupRecord] != 2 {
		t.Errorf("expected record=2, got %d", got[changes.SyncGroupRecord])
	}
	if got[changes.SyncGroupRoom] != 1 {
		t.Errorf("expected room=1, got %d", got[changes.SyncGroupRoom])
	}
	if got[changes.SyncGroupStaff] != 0 {
		t.Errorf("expected staff=0, got %d", got[changes.SyncGroupStaff])
	}
}

func TestSyncService_NilStoreIsSafe(t *testing.T) {
	var svc *changes.SyncService
	if err := svc.Bump(context.Background(), changes.SyncGroupRecord); err != nil {
		t.Fatalf("nil Bump returned error: %v", err)
	}
	if _, err := svc.Snapshot(context.Background()); err != nil {
		t.Fatalf("nil Snapshot returned error: %v", err)
	}
}
