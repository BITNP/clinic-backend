package testutil

import (
	"fmt"
	"testing"
	"time"

	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"

	"gorm.io/gorm"
)

func MustCreateRoom(t *testing.T, db *gorm.DB, id uint) {
	t.Helper()
	room := roommodel.ClinicRoom{ID: id, Address: "A", Enabled: true}
	room.Name = fmt.Sprintf("R-%d", id)
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room failed: %v", err)
	}
}

func FutureDateTime(daysFromNow int, hour, minute int) time.Time {
	d := FutureDate(daysFromNow)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, time.UTC)
}

func ValidServiceDateInput(roomID uint, daysFromNow int, cap uint) schedulingservice.CreateServiceDateInput {
	return schedulingservice.CreateServiceDateInput{
		Capacity:  cap,
		RoomID:    roomID,
		Date:      FutureDate(daysFromNow),
		StartTime: FutureDateTime(daysFromNow, 9, 0),
		EndTime:   FutureDateTime(daysFromNow, 12, 0),
		Title:     "Open Clinic",
	}
}

func MustCreateServiceDate(t *testing.T, db *gorm.DB, svc *schedulingservice.ServiceDateService, in schedulingservice.CreateServiceDateInput) schedulingmodel.ClinicServiceDate {
	t.Helper()
	MustEnsureRoom(t, db, in.RoomID)
	d, err := svc.Create(in)
	if err != nil {
		t.Fatalf("seed service date create failed: %v", err)
	}
	return d
}

// mustEnsureRoom inserts a room with the given id if it does not already exist.
func MustEnsureRoom(t *testing.T, db *gorm.DB, roomID uint) {
	t.Helper()
	var n int64
	if err := db.Model(&roommodel.ClinicRoom{}).Where("id = ?", roomID).Count(&n).Error; err != nil {
		t.Fatalf("count room %d: %v", roomID, err)
	}
	if n == 0 {
		MustCreateRoom(t, db, roomID)
	}
}
