package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

type reminderFixture struct {
	Room   roommodel.ClinicRoom
	Record recordmodel.ClinicRecord
	Date   schedulingmodel.ClinicServiceDate
	Start  time.Time
	End    time.Time
}

func seedReminderFixture(
	t *testing.T,
	db *gorm.DB,
	now time.Time,
	startOffset time.Duration,
	status recordmodel.RecordStatus,
) reminderFixture {
	t.Helper()
	room := roommodel.ClinicRoom{Name: "Reminder Room", Address: "Reminder Address", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	start := now.Add(startOffset).UTC()
	end := start.Add(2 * time.Hour)
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	date := schedulingmodel.ClinicServiceDate{
		Capacity:  10,
		RoomID:    &room.ID,
		Date:      day,
		StartTime: start,
		EndTime:   end,
		Title:     "test",
	}
	if err := db.Create(&date).Error; err != nil {
		t.Fatalf("seed service date: %v", err)
	}
	record := recordmodel.ClinicRecord{
		User:            "ding-user",
		Realname:        "guest",
		PhoneNum:        "13800000000",
		Status:          status,
		AppointmentTime: day,
		QuestionDesc:    "broken",
		RoomID:          room.ID,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}
	return reminderFixture{Room: room, Record: record, Date: date, Start: start, End: end}
}

func TestTicketReminderDiscoveryCreatesOneJobInsideLeadWindow(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		offset     time.Duration
		status     recordmodel.RecordStatus
		wantExists bool
	}{
		{name: "inside window", offset: 20 * time.Minute, status: recordmodel.RecordStatusConfirmed, wantExists: true},
		{name: "exact lead edge", offset: 30 * time.Minute, status: recordmodel.RecordStatusConfirmed, wantExists: true},
		{name: "outside window", offset: 31 * time.Minute, status: recordmodel.RecordStatusConfirmed, wantExists: false},
		{name: "already started", offset: -time.Minute, status: recordmodel.RecordStatusConfirmed, wantExists: false},
		{name: "not confirmed", offset: 20 * time.Minute, status: recordmodel.RecordStatusPending, wantExists: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupAdminRecordTestDB(t)
			fixture := seedReminderFixture(t, db, now, tt.offset, tt.status)
			discovery := &recordservice.TicketReminderDiscovery{DB: testutil.RawDB(t, db), LeadTime: 30 * time.Minute}

			created, err := discovery.Discover(context.Background(), now)
			if err != nil {
				t.Fatalf("Discover() error = %v", err)
			}
			if tt.wantExists {
				if created != 1 {
					t.Fatalf("created = %d, want 1", created)
				}
				var job recordmodel.ClinicRecordDingTalkReminder
				if err := db.Where("record_id = ?", fixture.Record.ID).First(&job).Error; err != nil {
					t.Fatalf("load job: %v", err)
				}
				if job.ServiceDateID != fixture.Date.ID || job.Status != "pending" {
					t.Errorf("job = %+v", job)
				}
				created, err = discovery.Discover(context.Background(), now)
				if err != nil || created != 0 {
					t.Fatalf("second Discover() = %d, %v; want 0, nil", created, err)
				}
				return
			}
			if created != 0 {
				t.Fatalf("created = %d, want 0", created)
			}
			var count int64
			if err := db.Model(&recordmodel.ClinicRecordDingTalkReminder{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("jobs = %d, want 0", count)
			}
		})
	}
}

func TestTicketReminderDeliverySendsAndMarksSent(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	db := setupAdminRecordTestDB(t)
	fixture := seedReminderFixture(t, db, now, 20*time.Minute, recordmodel.RecordStatusConfirmed)
	rawDB := testutil.RawDB(t, db)
	discovery := &recordservice.TicketReminderDiscovery{DB: rawDB, LeadTime: 30 * time.Minute}
	if _, err := discovery.Discover(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	notifier := &fakeTicketStatusNotifier{}
	delivery := &recordservice.TicketReminderDelivery{DB: rawDB, Notifier: notifier, Location: time.UTC}
	job, err := delivery.Claim(context.Background(), now)
	if err != nil || job == nil {
		t.Fatalf("Claim() = %+v, %v", job, err)
	}
	if err := delivery.Deliver(context.Background(), job, now); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(notifier.reminders) != 1 {
		t.Fatalf("reminders = %d, want 1", len(notifier.reminders))
	}
	got := notifier.reminders[0]
	if got.RecordID != fixture.Record.ID || got.User != fixture.Record.User ||
		got.RoomName != fixture.Room.Name || got.RoomAddress != fixture.Room.Address ||
		!got.ServiceStart.Equal(fixture.Start) || !got.ServiceEnd.Equal(fixture.End) {
		t.Errorf("reminder = %+v", got)
	}
	var stored recordmodel.ClinicRecordDingTalkReminder
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "sent" || stored.SentAt == nil || stored.ClaimToken != "" || stored.LeaseUntil != nil {
		t.Errorf("stored job = %+v", stored)
	}
}

func TestTicketReminderDiscoveryRearmsUnsentCancelledJob(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	db := setupAdminRecordTestDB(t)
	seedReminderFixture(t, db, now, 20*time.Minute, recordmodel.RecordStatusConfirmed)
	rawDB := testutil.RawDB(t, db)
	discovery := &recordservice.TicketReminderDiscovery{DB: rawDB, LeadTime: 30 * time.Minute}
	if _, err := discovery.Discover(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var job recordmodel.ClinicRecordDingTalkReminder
	if err := db.First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&job).Updates(map[string]any{
		"status":      "cancelled",
		"claim_token": "old-claim",
	}).Error; err != nil {
		t.Fatal(err)
	}
	created, err := discovery.Discover(context.Background(), now)
	if err != nil || created != 1 {
		t.Fatalf("Discover() = %d, %v; want 1, nil", created, err)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "pending" || job.ClaimToken != "" || job.Attempts != 0 {
		t.Errorf("rearmed job = %+v", job)
	}
}

func TestTicketReminderDeliveryCancelsWhenTicketNoLongerConfirmed(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	db := setupAdminRecordTestDB(t)
	fixture := seedReminderFixture(t, db, now, 20*time.Minute, recordmodel.RecordStatusConfirmed)
	rawDB := testutil.RawDB(t, db)
	discovery := &recordservice.TicketReminderDiscovery{DB: rawDB, LeadTime: 30 * time.Minute}
	if _, err := discovery.Discover(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&fixture.Record).Update("status", recordmodel.RecordStatusRejected).Error; err != nil {
		t.Fatal(err)
	}
	notifier := &fakeTicketStatusNotifier{}
	delivery := &recordservice.TicketReminderDelivery{DB: rawDB, Notifier: notifier, Location: time.UTC}
	job, err := delivery.Claim(context.Background(), now)
	if err != nil || job == nil {
		t.Fatalf("Claim() = %+v, %v", job, err)
	}
	if err := delivery.Deliver(context.Background(), job, now); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(notifier.reminders) != 0 {
		t.Fatalf("reminders = %d, want 0", len(notifier.reminders))
	}
	var stored recordmodel.ClinicRecordDingTalkReminder
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", stored.Status)
	}
}

func TestTicketReminderDeliveryRetriesFailure(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	db := setupAdminRecordTestDB(t)
	seedReminderFixture(t, db, now, 20*time.Minute, recordmodel.RecordStatusConfirmed)
	rawDB := testutil.RawDB(t, db)
	discovery := &recordservice.TicketReminderDiscovery{DB: rawDB, LeadTime: 30 * time.Minute}
	if _, err := discovery.Discover(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	notifier := &fakeTicketStatusNotifier{err: errors.New("message center unavailable")}
	delivery := &recordservice.TicketReminderDelivery{DB: rawDB, Notifier: notifier, Location: time.UTC}
	job, err := delivery.Claim(context.Background(), now)
	if err != nil || job == nil {
		t.Fatalf("Claim() = %+v, %v", job, err)
	}
	if err := delivery.Deliver(context.Background(), job, now); err == nil {
		t.Fatal("expected delivery error")
	}
	var stored recordmodel.ClinicRecordDingTalkReminder
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "pending" || stored.Attempts != 1 || !stored.NextAttemptAt.After(now) {
		t.Errorf("stored job = %+v", stored)
	}
}

func TestTicketReminderWorkerRunsOnStartup(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	db := setupAdminRecordTestDB(t)
	seedReminderFixture(t, db, now, 20*time.Minute, recordmodel.RecordStatusConfirmed)
	rawDB := testutil.RawDB(t, db)
	notifier := &fakeTicketStatusNotifier{}
	worker := &recordservice.TicketReminderWorker{
		Discovery:    &recordservice.TicketReminderDiscovery{DB: rawDB, LeadTime: 30 * time.Minute},
		Delivery:     &recordservice.TicketReminderDelivery{DB: rawDB, Notifier: notifier, Location: time.UTC},
		PollInterval: time.Hour,
		Now:          func() time.Time { return now },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var stored recordmodel.ClinicRecordDingTalkReminder
		err := db.First(&stored).Error
		if err == nil && stored.Status == "sent" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("worker did not send reminder; last error: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
	if len(notifier.reminders) != 1 {
		t.Fatalf("reminders = %d, want 1", len(notifier.reminders))
	}
}
