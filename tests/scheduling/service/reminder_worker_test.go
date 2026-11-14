package service_test

import (
	"context"
	"testing"
	"time"

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
