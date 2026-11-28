package model_test

import (
	schedulingmodel "clinic-backend/internal/scheduling/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReminderIdentityIsUnique(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&schedulingmodel.ServiceDateReminder{}); err != nil {
		t.Fatal(err)
	}
	job := schedulingmodel.ServiceDateReminder{ServiceDateID: 1, ServiceStartAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), LeadTimeSeconds: 3600, StaffID: 1, Status: "pending", NextAttemptAt: time.Now()}
	if err = db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	job.ID = 0
	if err = db.Create(&job).Error; err == nil {
		t.Fatal("duplicate identity accepted")
	}
	job.StaffID = 2
	if err = db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}

	completionJob := job
	completionJob.ID = 0
	completionJob.StaffID = 1
	completionJob.Kind = schedulingmodel.ReminderKindCompletion
	if err = db.Create(&completionJob).Error; err != nil {
		t.Fatalf("create completion reminder beside approval reminder: %v", err)
	}
	completionJob.ID = 0
	if err = db.Create(&completionJob).Error; err == nil {
		t.Fatal("duplicate completion reminder identity accepted")
	}
}
