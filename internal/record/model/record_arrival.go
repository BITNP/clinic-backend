package model

import (
	"time"
)

// ClinicRecordArrival records when the guest arrived at the clinic.
type ClinicRecordArrival struct {
	RecordID   uint      `gorm:"primaryKey;not null;column:record_id" json:"record_id"`
	ArriveTime time.Time `gorm:"not null;column:arrive_time" json:"arrive_time"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordArrival) TableName() string {
	return "clinic_record_arrival"
}
