package models

import "time"

// ClinicServiceDate represents a single clinic service window on a specific date and room.
type ClinicServiceDate struct {
	ID        uint      `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Capacity  uint      `gorm:"not null;column:capacity;check:,capacity >= 0" json:"capacity"`
	RoomID    *uint     `gorm:"column:room_id;uniqueIndex:idx_room_date" json:"room_id"`
	Date      time.Time `gorm:"type:date;not null;column:date;uniqueIndex:idx_room_date" json:"date"`
	StartTime time.Time `gorm:"type:timestamp;not null;column:start_time" json:"startTime"`
	EndTime   time.Time `gorm:"type:timestamp;not null;column:end_time" json:"endTime"`
	Title     string    `gorm:"size:20;not null;column:title" json:"title"`
	Count     int64     `gorm:"-" json:"count"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicServiceDate) TableName() string {
	return "clinic_service_date"
}
