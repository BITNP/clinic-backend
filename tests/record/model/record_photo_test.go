package model_test

import (
	"testing"

	recordmodel "clinic-backend/internal/record/model"
)

func TestClinicRecordPhoto_CreateAndRetrieve(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)

	photo := recordmodel.ClinicRecordPhoto{
		RecordID:    record.ID,
		ObjectKey:   "photos/records/1/photo.jpg",
		ContentType: "image/jpeg",
		Size:        204800,
		Position:    1,
	}
	if err := db.Create(&photo).Error; err != nil {
		t.Fatalf("failed to create photo: %v", err)
	}

	var retrieved recordmodel.ClinicRecordPhoto
	if err := db.Where("record_id = ? AND position = ?", record.ID, 1).First(&retrieved).Error; err != nil {
		t.Fatalf("failed to retrieve photo: %v", err)
	}

	if retrieved.ID != photo.ID {
		t.Errorf("expected id %d, got %d", photo.ID, retrieved.ID)
	}
	if retrieved.RecordID != record.ID {
		t.Errorf("expected record_id %d, got %d", record.ID, retrieved.RecordID)
	}
	if retrieved.ObjectKey != photo.ObjectKey {
		t.Errorf("expected object_key %q, got %q", photo.ObjectKey, retrieved.ObjectKey)
	}
	if retrieved.ContentType != photo.ContentType {
		t.Errorf("expected content_type %q, got %q", photo.ContentType, retrieved.ContentType)
	}
	if retrieved.Size != photo.Size {
		t.Errorf("expected size %d, got %d", photo.Size, retrieved.Size)
	}
	if retrieved.Position != photo.Position {
		t.Errorf("expected position %d, got %d", photo.Position, retrieved.Position)
	}
	if retrieved.CreatedAt.IsZero() {
		t.Error("expected created_at to be set")
	}
}

func TestClinicRecordPhoto_OrderedByPosition(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)

	for _, position := range []int{2, 1} {
		photo := recordmodel.ClinicRecordPhoto{
			RecordID:    record.ID,
			ObjectKey:   "photos/records/1/photo.jpg",
			ContentType: "image/jpeg",
			Size:        1,
			Position:    position,
		}
		if err := db.Create(&photo).Error; err != nil {
			t.Fatalf("failed to create photo at %d: %v", position, err)
		}
	}

	var photos []recordmodel.ClinicRecordPhoto
	if err := db.Where("record_id = ?", record.ID).Order("position ASC").Find(&photos).Error; err != nil {
		t.Fatalf("failed to list photos: %v", err)
	}
	if len(photos) != 2 {
		t.Fatalf("photos = %d, want 2", len(photos))
	}
	if photos[0].Position != 1 || photos[1].Position != 2 {
		t.Fatalf("positions = %d/%d, want 1/2", photos[0].Position, photos[1].Position)
	}
}

func TestClinicRecordPhoto_PositionIndexExists(t *testing.T) {
	db := setupRecordTestDB(t)

	if !db.Migrator().HasIndex(&recordmodel.ClinicRecordPhoto{}, "idx_record_photo_position") {
		t.Fatal("expected idx_record_photo_position index to exist")
	}
}
