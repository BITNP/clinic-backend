package model

import (
	staffmodel "clinic-backend/internal/staff/model"
)

type ClinicWorkScheduleStaff struct {
	ID         uint                      `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	WeekdayID  uint                      `gorm:"not null;column:weekday_id" json:"weekday_id"`
	StaffID    int                       `gorm:"not null;column:staff_id" json:"staff_id"`
	ScheduleID uint                      `gorm:"not null;default:0;column:schedule_id" json:"schedule_id"`
	Weekday    ClinicWorkScheduleWeekday `gorm:"foreignKey:WeekdayID" json:"-"`
	Staff      staffmodel.ClinicStaff    `gorm:"foreignKey:StaffID" json:"staff,omitempty"`
	Schedule   ClinicWorkSchedule        `gorm:"foreignKey:ScheduleID" json:"-"`
}

func (ClinicWorkScheduleStaff) TableName() string {
	return "clinic_work_schedule_staff"
}
