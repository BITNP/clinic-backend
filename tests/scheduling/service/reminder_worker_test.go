package service_test

import (
	"context"
	"testing"
	"time"

	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
	"clinic-backend/internal/testutil"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReminderWorkerStopsOnCancellation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rawDB := testutil.RawDB(t, db)
	worker := schedulingservice.ReminderWorker{Discovery: &schedulingservice.ReminderDiscovery{DB: rawDB}, Delivery: &schedulingservice.ReminderDelivery{DB: rawDB}}
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestReminderDeliveryPausesAndCancelsRemovedLead(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&schedulingmodel.ServiceDateReminder{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := schedulingmodel.ServiceDateReminder{ServiceDateID: 1, ServiceStartAt: now.Add(time.Hour), LeadTimeSeconds: 3600, StaffID: 1, Status: "pending", NextAttemptAt: now}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	enabled := false
	delivery := schedulingservice.ReminderDelivery{DB: testutil.RawDB(t, db), Lease: time.Minute, Settings: func() (bool, []time.Duration) { return enabled, []time.Duration{30 * time.Minute} }}
	claimed, err := delivery.Claim(context.Background(), now)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := delivery.Deliver(context.Background(), claimed, now); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&job, job.ID).Error; err != nil || job.Status != "pending" {
		t.Fatalf("paused job: %+v, %v", job, err)
	}
	enabled = true
	claimed, err = delivery.Claim(context.Background(), now)
	if err != nil || claimed == nil {
		t.Fatalf("reclaim: %v", err)
	}
	if err := delivery.Deliver(context.Background(), claimed, now); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&job, job.ID).Error; err != nil || job.Status != "cancelled" {
		t.Fatalf("removed lead job: %+v, %v", job, err)
	}
}
