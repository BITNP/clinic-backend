package tests

import (
	"testing"
	"time"

	"clinic-backend/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupServiceDateTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}

	if err := db.AutoMigrate(&models.ClinicRoom{}, &models.ClinicServiceDate{}); err != nil {
		t.Fatalf("failed to migrate service date models: %v", err)
	}

	return db
}

func createTestRoomForServiceDate(t *testing.T, db *gorm.DB) models.ClinicRoom {
	room := models.ClinicRoom{Name: "Main Clinic", Address: "123 Campus Dr"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	return room
}

func TestClinicServiceDate_CreateAndRetrieve(t *testing.T) {
	db := setupServiceDateTestDB(t)
	room := createTestRoomForServiceDate(t, db)

	date := time.Now().UTC().Truncate(24 * time.Hour)
	start := time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC)
	end := time.Date(0, 1, 1, 17, 0, 0, 0, time.UTC)

	serviceDate := models.ClinicServiceDate{
		Capacity:  20,
		RoomID:    &room.ID,
		Date:      date,
		StartTime: start,
		EndTime:   end,
		Title:     "Walk-in Day",
	}
	if err := db.Create(&serviceDate).Error; err != nil {
		t.Fatalf("failed to create service date: %v", err)
	}

	if serviceDate.ID == 0 {
		t.Fatal("expected auto-incremented ID to be assigned")
	}

	var retrieved models.ClinicServiceDate
	if err := db.First(&retrieved, serviceDate.ID).Error; err != nil {
		t.Fatalf("failed to retrieve service date: %v", err)
	}

	if retrieved.Capacity != serviceDate.Capacity {
		t.Errorf("expected capacity %d, got %d", serviceDate.Capacity, retrieved.Capacity)
	}
	if retrieved.RoomID == nil || *retrieved.RoomID != room.ID {
		t.Errorf("expected room_id %d, got %v", room.ID, retrieved.RoomID)
	}
	if !retrieved.Date.Equal(date) {
		t.Errorf("expected date %v, got %v", date, retrieved.Date)
	}
	if retrieved.StartTime.Format("15:04:05") != start.Format("15:04:05") {
		t.Errorf("expected startTime %v, got %v", start, retrieved.StartTime)
	}
	if retrieved.EndTime.Format("15:04:05") != end.Format("15:04:05") {
		t.Errorf("expected endTime %v, got %v", end, retrieved.EndTime)
	}
	if retrieved.Title != serviceDate.Title {
		t.Errorf("expected title %q, got %q", serviceDate.Title, retrieved.Title)
	}
}

func TestClinicServiceDate_NullableRoom(t *testing.T) {
	db := setupServiceDateTestDB(t)

	serviceDate := models.ClinicServiceDate{
		Capacity:  10,
		RoomID:    nil,
		Date:      time.Now().UTC().Truncate(24 * time.Hour),
		StartTime: time.Date(0, 1, 1, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(0, 1, 1, 16, 0, 0, 0, time.UTC),
		Title:     "Online Clinic",
	}
	if err := db.Create(&serviceDate).Error; err != nil {
		t.Fatalf("failed to create service date without room: %v", err)
	}

	var retrieved models.ClinicServiceDate
	if err := db.First(&retrieved, serviceDate.ID).Error; err != nil {
		t.Fatalf("failed to retrieve service date: %v", err)
	}
	if retrieved.RoomID != nil {
		t.Errorf("expected nil room_id, got %v", *retrieved.RoomID)
	}
}

func TestClinicServiceDate_UniqueRoomDate(t *testing.T) {
	db := setupServiceDateTestDB(t)
	room := createTestRoomForServiceDate(t, db)
	date := time.Now().UTC().Truncate(24 * time.Hour)
	start := time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC)
	end := time.Date(0, 1, 1, 17, 0, 0, 0, time.UTC)

	first := models.ClinicServiceDate{
		Capacity:  20,
		RoomID:    &room.ID,
		Date:      date,
		StartTime: start,
		EndTime:   end,
		Title:     "First",
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("failed to create first service date: %v", err)
	}

	second := models.ClinicServiceDate{
		Capacity:  30,
		RoomID:    &room.ID,
		Date:      date,
		StartTime: start,
		EndTime:   end,
		Title:     "Second",
	}
	if err := db.Create(&second).Error; err == nil {
		t.Fatal("expected duplicate (room_id, date) to fail")
	}
}

func TestClinicServiceDate_NegativeCapacityCheck(t *testing.T) {
	db := setupServiceDateTestDB(t)
	room := createTestRoomForServiceDate(t, db)
	date := time.Now().UTC().Truncate(24 * time.Hour)
	start := time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC)
	end := time.Date(0, 1, 1, 17, 0, 0, 0, time.UTC)

	err := db.Exec(
		`INSERT INTO clinic_service_date (capacity, room_id, date, start_time, end_time, title) VALUES (?, ?, ?, ?, ?, ?)`,
		-1, room.ID, date.Format("2006-01-02"), start.Format("15:04:05"), end.Format("15:04:05"), "Bad",
	).Error
	if err == nil {
		t.Fatal("expected negative capacity to be rejected by check constraint")
	}
}
