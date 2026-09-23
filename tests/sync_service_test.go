package tests

import (
	"context"
	"sync"
	"testing"

	"clinic-backend/services"
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

var testSyncGroups = []services.SyncGroup{
	services.SyncGroupRecord,
	services.SyncGroupRoom,
	services.SyncGroupWorkSchedule,
	services.SyncGroupServiceDate,
	services.SyncGroupStaff,
	services.SyncGroupAnnouncement,
}

func TestSyncService_Snapshot_DefaultsToZero(t *testing.T) {
	svc := services.NewSyncService(newFakeSyncStore())

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
	svc := services.NewSyncService(newFakeSyncStore())
	ctx := context.Background()

	if err := svc.Bump(ctx, services.SyncGroupRecord); err != nil {
		t.Fatalf("bump record: %v", err)
	}
	if err := svc.Bump(ctx, services.SyncGroupRecord); err != nil {
		t.Fatalf("bump record: %v", err)
	}
	if err := svc.Bump(ctx, services.SyncGroupRoom); err != nil {
		t.Fatalf("bump room: %v", err)
	}

	got, err := svc.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if got[services.SyncGroupRecord] != 2 {
		t.Errorf("expected record=2, got %d", got[services.SyncGroupRecord])
	}
	if got[services.SyncGroupRoom] != 1 {
		t.Errorf("expected room=1, got %d", got[services.SyncGroupRoom])
	}
	if got[services.SyncGroupStaff] != 0 {
		t.Errorf("expected staff=0, got %d", got[services.SyncGroupStaff])
	}
}

func TestSyncService_NilStoreIsSafe(t *testing.T) {
	var svc *services.SyncService
	if err := svc.Bump(context.Background(), services.SyncGroupRecord); err != nil {
		t.Fatalf("nil Bump returned error: %v", err)
	}
	if _, err := svc.Snapshot(context.Background()); err != nil {
		t.Fatalf("nil Snapshot returned error: %v", err)
	}
}
