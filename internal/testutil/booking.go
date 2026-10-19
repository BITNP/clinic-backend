package testutil

import (
	"errors"
	"testing"
	"time"

	"clinic-backend/internal/calendar"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"

	"gorm.io/gorm"
)

// seedOpenServiceDate inserts (idempotently) a Room named "中关村" and a
// ServiceDate on `date` with the given capacity. The date is normalized using
// the same timezone-aware rule as the service layer so that lookup keys match.
// Returns the room id.
func SeedOpenServiceDate(t *testing.T, db *gorm.DB, loc *time.Location, date time.Time, capacity uint) uint {
	t.Helper()
	var room roommodel.ClinicRoom
	if err := db.Where("name = ?", "中关村").First(&room).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("lookup room: %v", err)
		}
		room = roommodel.ClinicRoom{Name: "中关村", Address: "addr", Enabled: true}
		if err := db.Create(&room).Error; err != nil {
			t.Fatalf("seed room: %v", err)
		}
	}
	d := schedulingmodel.ClinicServiceDate{
		Capacity:  capacity,
		RoomID:    &room.ID,
		Date:      calendar.DateInLocation(date, loc),
		StartTime: date.Add(9 * time.Hour),
		EndTime:   date.Add(17 * time.Hour),
		Title:     "open",
	}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("seed service date: %v", err)
	}
	return room.ID
}

func FutureTruncatedDate(days int) time.Time {
	return time.Now().UTC().AddDate(0, 0, days).Truncate(24 * time.Hour)
}
