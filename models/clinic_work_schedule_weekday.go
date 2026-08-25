package models

import "time"

type ClinicWorkScheduleWeekday struct {
	ID             uint                      `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	WorkScheduleID uint                      `gorm:"not null;column:work_schedule_id" json:"work_schedule_id"`
	Weekday        int                       `gorm:"not null;column:weekday" json:"weekday"`
	StartTime      time.Time                 `gorm:"type:timestamp;not null;column:start_time" json:"start_time"`
	EndTime        time.Time                 `gorm:"type:timestamp;not null;column:end_time" json:"end_time"`
	RoomID         uint                      `gorm:"not null;column:room_id" json:"room_id"`
	WorkSchedule   ClinicWorkSchedule        `gorm:"foreignKey:WorkScheduleID" json:"-"`
	Room           ClinicRoom                `gorm:"foreignKey:RoomID" json:"room,omitempty"`
	Staff          []ClinicWorkScheduleStaff `gorm:"foreignKey:WeekdayID" json:"staff,omitempty"`
}

func (ClinicWorkScheduleWeekday) TableName() string {
	return "clinic_work_schedule_weekday"
}
