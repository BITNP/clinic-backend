package models

import "time"

// RecordStatus represents the lifecycle state of a clinic repair record.
type RecordStatus string

const (
	RecordStatusPending    RecordStatus = "pending"
	RecordStatusConfirmed  RecordStatus = "confirmed"
	RecordStatusArrived    RecordStatus = "arrived"
	RecordStatusInProgress RecordStatus = "in_progress"
	RecordStatusCompleted  RecordStatus = "completed"
	RecordStatusRejected   RecordStatus = "rejected"
	RecordStatusReferred   RecordStatus = "referred"
	RecordStatusNoShow     RecordStatus = "no_show"
)

// ClinicRecord is the core repair ticket submitted by a student.
// Optional workflow data lives in the related one-to-one side tables.
type ClinicRecord struct {
	ID              uint         `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	User            string       `gorm:"size:50;column:user" json:"user"`
	Realname        string       `gorm:"size:50;not null;column:realname" json:"realname"`
	PhoneNum        string       `gorm:"size:20;not null;column:phone_num" json:"phone_num"`
	Status          RecordStatus `gorm:"type:varchar(20);not null;column:status" json:"status"`
	AppointmentTime time.Time    `gorm:"type:date;not null;column:appointment_time" json:"appointment_time"`
	QuestionDesc    string       `gorm:"size:10000;not null;column:question_desc" json:"question_desc"`
	RoomID          uint         `gorm:"not null;column:room" json:"room"`
	ApproverID      *uint        `gorm:"column:approver_id" json:"approver_id"`
	TagID           uint         `gorm:"column:tag_id" json:"tag_id"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecord) TableName() string {
	return "clinic_record"
}
