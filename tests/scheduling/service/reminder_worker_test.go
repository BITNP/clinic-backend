package service_test

import (
	"context"
	"testing"
	"time"

	schedulingservice "clinic-backend/internal/scheduling/service"
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
	worker := schedulingservice.ReminderWorker{Discovery: &schedulingservice.ReminderDiscovery{DB: db}, Delivery: &schedulingservice.ReminderDelivery{DB: db}}
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}
