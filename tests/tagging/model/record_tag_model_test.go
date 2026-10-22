package model_test

import (
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	taggingmodel "clinic-backend/internal/tagging/model"
	"clinic-backend/internal/testutil"
)

func TestClinicRecordTag_CreateAndRetrieve(t *testing.T) {
	db := testutil.SetupTagTestDB(t)

	tag := taggingmodel.ClinicRecordTag{
		Title:       "no_pending",
		Description: "record awaiting processing",
		ApplyRule:   "status == pending",
	}
	if err := db.Create(&tag).Error; err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if tag.ID == 0 {
		t.Fatal("expected id assigned")
	}

	var got taggingmodel.ClinicRecordTag
	if err := db.First(&got, tag.ID).Error; err != nil {
		t.Fatalf("retrieve tag: %v", err)
	}
	if got.Title != tag.Title || got.Description != tag.Description || got.ApplyRule != tag.ApplyRule {
		t.Errorf("tag mismatch: %+v", got)
	}
}

func TestClinicRecord_PersistsTagID(t *testing.T) {
	db := testutil.SetupTagTestDB(t)

	rec := recordmodel.ClinicRecord{
		Realname:        "Student A",
		PhoneNum:        "555-0100",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: time.Now().UTC().Truncate(24 * time.Hour),
		QuestionDesc:    "broken",
		RoomID:          1,
		TagID:           7,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}

	var got recordmodel.ClinicRecord
	if err := db.First(&got, rec.ID).Error; err != nil {
		t.Fatalf("retrieve record: %v", err)
	}
	if got.TagID != 7 {
		t.Errorf("expected tag_id 7, got %d", got.TagID)
	}
}
