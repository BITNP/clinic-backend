package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/changes"
	"clinic-backend/internal/journal"
	journalmodel "clinic-backend/internal/journal/model"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

type reminderSyncStore struct {
	counters map[string]int64
}

func (s *reminderSyncStore) Incr(_ context.Context, key string) (int64, error) {
	if s.counters == nil {
		s.counters = map[string]int64{}
	}
	s.counters[key]++
	return s.counters[key], nil
}

func (s *reminderSyncStore) Get(_ context.Context, keys []string) (map[string]int64, error) {
	out := make(map[string]int64, len(keys))
	for _, key := range keys {
		out[key] = s.counters[key]
	}
	return out, nil
}

func setupDingTalkReminderJournal(
	t *testing.T,
) (*recordservice.TicketReminderDiscovery, *recordservice.TicketReminderDelivery, *gorm.DB, *changes.SyncService) {
	t.Helper()
	db := setupAdminRecordTestDB(t)
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	rawDB := testutil.RawDB(t, db)
	journalSvc := journal.NewService(rawDB)
	syncSvc := changes.NewSyncService(&reminderSyncStore{})
	journalSvc.SetSyncService(syncSvc)
	return &recordservice.TicketReminderDiscovery{
		DB:       rawDB,
		LeadTime: 30 * time.Minute,
		Journal:  journalSvc,
	}, &recordservice.TicketReminderDelivery{
		DB:       rawDB,
		Notifier: &fakeTicketStatusNotifier{},
		Location: time.UTC,
		Journal:  journalSvc,
	}, db, syncSvc
}

func TestDingTalkReminderJournalsRequestAttemptAndSent(t *testing.T) {
	discovery, delivery, db, syncSvc := setupDingTalkReminderJournal(t)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	fixture := seedReminderFixture(t, db, now, 20*time.Minute, recordmodel.RecordStatusConfirmed)
	if created, err := discovery.Discover(context.Background(), now); err != nil || created != 1 {
		t.Fatalf("discover = %d, %v", created, err)
	}

	var requested journalmodel.Event
	if err := db.Order("id").First(&requested).Error; err != nil {
		t.Fatalf("load requested event: %v", err)
	}
	if requested.Action != "notification.requested" || requested.EntityID != testutil.Itoa(fixture.Record.ID) {
		t.Fatalf("requested event = %+v", requested)
	}

	notifier := &fakeTicketStatusNotifier{}
	delivery.Notifier = notifier
	job, err := delivery.Claim(context.Background(), now)
	if err != nil || job == nil {
		t.Fatalf("claim = %+v, %v", job, err)
	}
	if job.RequestedEventID == nil || *job.RequestedEventID != requested.ID || job.AttemptEventID == nil {
		t.Fatalf("claimed job linkage = %+v", job)
	}
	attemptID := *job.AttemptEventID

	if err := delivery.Deliver(context.Background(), job, now); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	actions := make([]string, len(events))
	for i, event := range events {
		actions[i] = event.Action
	}
	want := []string{"notification.requested", "notification.attempt_started", "notification.sent"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
	if events[1].CausedByEventID == nil || *events[1].CausedByEventID != requested.ID {
		t.Fatalf("attempt cause = %v, want %d", events[1].CausedByEventID, requested.ID)
	}
	if events[2].CausedByEventID == nil || *events[2].CausedByEventID != attemptID {
		t.Fatalf("sent cause = %v, want %d", events[2].CausedByEventID, attemptID)
	}
	var sentDetails map[string]any
	if err := json.Unmarshal(events[2].Details, &sentDetails); err != nil {
		t.Fatalf("decode sent details: %v", err)
	}
	if sentDetails["message_center_msg_id"] != "ticket-reminder-message-1" {
		t.Fatalf("sent details = %+v", sentDetails)
	}
	var stored recordmodel.ClinicRecordDingTalkReminder
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("load reminder: %v", err)
	}
	if stored.Status != "sent" || len(notifier.reminders) != 1 {
		t.Fatalf("stored = %+v, notifications = %d", stored, len(notifier.reminders))
	}
	counters, err := syncSvc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot journal sync: %v", err)
	}
	if counters[changes.SyncGroupJournal] != 3 {
		t.Fatalf("journal sync counter = %d, want 3", counters[changes.SyncGroupJournal])
	}
}

func TestDingTalkReminderJournalsRetryWithoutRawError(t *testing.T) {
	discovery, delivery, db, _ := setupDingTalkReminderJournal(t)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	seedReminderFixture(t, db, now, 20*time.Minute, recordmodel.RecordStatusConfirmed)
	if _, err := discovery.Discover(context.Background(), now); err != nil {
		t.Fatalf("discover: %v", err)
	}
	delivery.Notifier = &fakeTicketStatusNotifier{err: errors.New("message center unavailable: secret@example.com")}
	job, err := delivery.Claim(context.Background(), now)
	if err != nil || job == nil {
		t.Fatalf("claim = %+v, %v", job, err)
	}
	var claimedEvents []journalmodel.Event
	if err := db.Order("id").Find(&claimedEvents).Error; err != nil {
		t.Fatalf("load claimed events: %v", err)
	}
	if len(claimedEvents) != 2 || claimedEvents[1].Action != "notification.attempt_started" {
		t.Fatalf("claimed events = %+v", claimedEvents)
	}
	if err := delivery.Deliver(context.Background(), job, now); err == nil {
		t.Fatal("expected notifier error")
	}
	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load retry events: %v", err)
	}
	event := events[len(events)-1]
	if event.Action != "notification.retry_scheduled" {
		t.Fatalf("action = %q", event.Action)
	}
	if strings.Contains(string(event.Details), "secret@example.com") {
		t.Fatalf("journal leaked raw error: %s", event.Details)
	}
}

func TestDingTalkReminderDiscoveryRollsBackWhenJournalFails(t *testing.T) {
	discovery, _, db, syncSvc := setupDingTalkReminderJournal(t)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	fixture := seedReminderFixture(t, db, now, 20*time.Minute, recordmodel.RecordStatusConfirmed)
	if err := db.Exec(`
		CREATE TRIGGER reject_dingtalk_reminder_journal
		BEFORE INSERT ON clinic_journal_event
		BEGIN
			SELECT RAISE(ABORT, 'journal unavailable');
		END
	`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if _, err := discovery.Discover(context.Background(), now); err == nil {
		t.Fatal("expected discovery to fail")
	}
	var count int64
	if err := db.Model(&recordmodel.ClinicRecordDingTalkReminder{}).
		Where("record_id = ?", fixture.Record.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("reminder rows = %d, want 0", count)
	}
	counters, err := syncSvc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot journal sync: %v", err)
	}
	if counters[changes.SyncGroupJournal] != 0 {
		t.Fatalf("journal sync counter = %d after rollback, want 0", counters[changes.SyncGroupJournal])
	}
}
