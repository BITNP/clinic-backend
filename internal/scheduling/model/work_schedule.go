package model

import (
	"time"
)

type ClinicWorkSchedule struct {
	ID        uint                        `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name      string                      `gorm:"size:128;not null;uniqueIndex;column:name" json:"name"`
	StartDate time.Time                   `gorm:"type:date;not null;column:start_date" json:"start_date"`
	EndDate   time.Time                   `gorm:"type:date;not null;column:end_date" json:"end_date"`
	Enabled   bool                        `gorm:"not null;default:false;column:enabled" json:"enabled"`
	Weekdays  []ClinicWorkScheduleWeekday `gorm:"foreignKey:WorkScheduleID" json:"weekdays,omitempty"`
}

func (ClinicWorkSchedule) TableName() string {
	return "clinic_work_schedule"
}
