package journal_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"clinic-backend/internal/changes"
	"clinic-backend/internal/journal"
	journalmodel "clinic-backend/internal/journal/model"
	"clinic-backend/internal/testutil"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type journalSyncStore struct {
	counters map[string]int64
}

func (s *journalSyncStore) Incr(_ context.Context, key string) (int64, error) {
	if s.counters == nil {
		s.counters = map[string]int64{}
	}
	s.counters[key]++
	return s.counters[key], nil
}

func (s *journalSyncStore) Get(_ context.Context, keys []string) (map[string]int64, error) {
	out := make(map[string]int64, len(keys))
	for _, key := range keys {
		out[key] = s.counters[key]
	}
	return out, nil
}

func setupJournalDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	return db
}

func TestServiceBumpSync(t *testing.T) {
	syncSvc := changes.NewSyncService(&journalSyncStore{})
	svc := journal.NewService(testutil.RawDB(t, setupJournalDB(t)))
	svc.SetSyncService(syncSvc)

	if err := svc.BumpSync(context.Background()); err != nil {
		t.Fatalf("bump journal sync: %v", err)
	}
	counters, err := syncSvc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot sync counters: %v", err)
	}
	if counters[changes.SyncGroupJournal] != 1 {
		t.Fatalf("journal counter = %d, want 1", counters[changes.SyncGroupJournal])
	}
}

func TestInsertAndListEvents(t *testing.T) {
	db := setupJournalDB(t)
	rawDB := testutil.RawDB(t, db)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	tx, err := rawDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	firstID, err := journal.InsertTx(context.Background(), tx, journal.EventInput{
		OccurredAt:  now,
		ActorType:   journal.ActorStaff,
		ActorID:     "42",
		Action:      "record.confirmed",
		EntityType:  "record",
		EntityID:    "123",
		OperationID: "op-a",
		Details: map[string]any{
			"version": 1,
			"changes": map[string]any{
				"status": map[string]any{"before": "pending", "after": "confirmed"},
			},
		},
	})
	if err != nil {
		tx.Rollback()
		t.Fatalf("insert first event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit first event: %v", err)
	}

	tx, err = rawDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin second: %v", err)
	}
	secondID, err := journal.InsertTx(context.Background(), tx, journal.EventInput{
		OccurredAt:  now.Add(time.Minute),
		ActorType:   journal.ActorStaff,
		ActorID:     "42",
		Action:      "record.notes_updated",
		EntityType:  "record",
		EntityID:    "123",
		OperationID: "op-b",
		Details:     map[string]any{"version": 1, "changes": map[string]any{}},
	})
	if err != nil {
		tx.Rollback()
		t.Fatalf("insert second event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit second event: %v", err)
	}
	if secondID <= firstID {
		t.Fatalf("event IDs are not increasing: first=%d second=%d", firstID, secondID)
	}

	svc := journal.NewService(rawDB)
	page, err := svc.List(context.Background(), journal.Filter{Limit: 1})
	if err != nil {
		t.Fatalf("list journal: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != secondID || page.NextCursor == nil ||
		*page.NextCursor != secondID {
		t.Fatalf("first page = %+v", page)
	}

	page, err = svc.List(context.Background(), journal.Filter{Limit: 10, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != firstID || page.NextCursor != nil {
		t.Fatalf("second page = %+v", page)
	}
	var details map[string]any
	if err := json.Unmarshal(page.Items[0].Details, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details["version"] != float64(1) {
		t.Fatalf("details version = %#v", details["version"])
	}
}

func TestInsertTxRollsBackWithBusinessChange(t *testing.T) {
	db := setupJournalDB(t)
	rawDB := testutil.RawDB(t, db)
	if err := db.Exec("CREATE TABLE test_business (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create business table: %v", err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_journal_event
		BEFORE INSERT ON clinic_journal_event
		BEGIN
			SELECT RAISE(ABORT, 'journal unavailable');
		END
	`).Error; err != nil {
		t.Fatalf("create rejection trigger: %v", err)
	}

	tx, err := rawDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO test_business (id) VALUES (1)"); err != nil {
		tx.Rollback()
		t.Fatalf("insert business row: %v", err)
	}
	if _, err := journal.InsertTx(context.Background(), tx, journal.EventInput{
		ActorType:   journal.ActorSystem,
		ActorID:     "worker",
		Action:      "test.event",
		EntityType:  "test",
		EntityID:    "1",
		OperationID: "op-test",
	}); err == nil {
		tx.Rollback()
		t.Fatal("expected invalid insert to fail")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int64
	if err := db.Model(&journalmodel.Event{}).Count(&count).Error; err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Fatalf("event count = %d, want 0", count)
	}
	if err := db.Raw("SELECT COUNT(*) FROM test_business").Scan(&count).Error; err != nil {
		t.Fatalf("count business rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("business row count = %d, want 0", count)
	}
}
