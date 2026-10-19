package model_test

import (
	"testing"

	roommodel "clinic-backend/internal/room/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}

	if err := db.AutoMigrate(&roommodel.ClinicRoom{}); err != nil {
		t.Fatalf("failed to migrate ClinicRoom model: %v", err)
	}

	return db
}

func TestClinicRoom_CreateAndRetrieve(t *testing.T) {
	db := setupTestDB(t)

	room := roommodel.ClinicRoom{
		Name:    "Main Campus Clinic",
		Address: "123 University Ave, Building A",
	}

	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("failed to create clinic room: %v", err)
	}

	if room.ID == 0 {
		t.Fatal("expected auto-incremented ID to be assigned")
	}

	var retrieved roommodel.ClinicRoom
	if err := db.First(&retrieved, room.ID).Error; err != nil {
		t.Fatalf("failed to retrieve clinic room: %v", err)
	}

	if retrieved.Name != room.Name {
		t.Errorf("expected name %q, got %q", room.Name, retrieved.Name)
	}
	if retrieved.Address != room.Address {
		t.Errorf("expected address %q, got %q", room.Address, retrieved.Address)
	}
}

func TestClinicRoom_Update(t *testing.T) {
	db := setupTestDB(t)

	room := roommodel.ClinicRoom{Name: "Old Name", Address: "Old Address"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("failed to create clinic room: %v", err)
	}

	if err := db.Model(&room).Updates(roommodel.ClinicRoom{Name: "New Name", Address: "New Address"}).Error; err != nil {
		t.Fatalf("failed to update clinic room: %v", err)
	}

	var updated roommodel.ClinicRoom
	if err := db.First(&updated, room.ID).Error; err != nil {
		t.Fatalf("failed to retrieve updated clinic room: %v", err)
	}

	if updated.Name != "New Name" {
		t.Errorf("expected name %q, got %q", "New Name", updated.Name)
	}
	if updated.Address != "New Address" {
		t.Errorf("expected address %q, got %q", "New Address", updated.Address)
	}
}

func TestClinicRoom_Delete(t *testing.T) {
	db := setupTestDB(t)

	room := roommodel.ClinicRoom{Name: "To Be Deleted", Address: "Nowhere"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("failed to create clinic room: %v", err)
	}

	if err := db.Delete(&room).Error; err != nil {
		t.Fatalf("failed to delete clinic room: %v", err)
	}

	var count int64
	db.Model(&roommodel.ClinicRoom{}).Where("id = ?", room.ID).Count(&count)
	if count != 0 {
		t.Errorf("expected clinic room to be deleted, found %d record(s)", count)
	}
}

func TestClinicRoom_AutoIncrementID(t *testing.T) {
	db := setupTestDB(t)

	rooms := []roommodel.ClinicRoom{
		{Name: "Room 1", Address: "Address 1"},
		{Name: "Room 2", Address: "Address 2"},
		{Name: "Room 3", Address: "Address 3"},
	}

	for i := range rooms {
		if err := db.Create(&rooms[i]).Error; err != nil {
			t.Fatalf("failed to create clinic room %d: %v", i, err)
		}
	}

	for i, room := range rooms {
		expectedID := uint(i + 1)
		if room.ID != expectedID {
			t.Errorf("expected room %d to have ID %d, got %d", i, expectedID, room.ID)
		}
	}
}
