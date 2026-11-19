package model_test

import (
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestClinicRecordDingTalkReminderIdentityIsUnique(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&recordmodel.ClinicRecordDingTalkReminder{}); err != nil {
		t.Fatal(err)
	}
	first := recordmodel.ClinicRecordDingTalkReminder{
		RecordID:      1,
		ServiceDateID: 2,
		Status:        "pending",
		NextAttemptAt: time.Now().UTC(),
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.ID = 0
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("expected duplicate reminder identity to be rejected")
	}
}
