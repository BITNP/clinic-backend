package model

import "time"

// ClinicRecordDingTalkReminder persists one guest reminder delivery stream for
// a record's service window.
type ClinicRecordDingTalkReminder struct {
	ID               uint       `gorm:"primaryKey;autoIncrement;column:id"`
	RecordID         uint       `gorm:"not null;uniqueIndex:idx_record_dingtalk_reminder_identity,priority:1;column:record_id"`
	ServiceDateID    uint       `gorm:"not null;uniqueIndex:idx_record_dingtalk_reminder_identity,priority:2;column:service_date_id"`
	Status           string     `gorm:"size:16;not null;index:idx_record_dingtalk_reminder_due,priority:1;column:status"`
	Attempts         int        `gorm:"not null;default:0;column:attempts"`
	NextAttemptAt    time.Time  `gorm:"not null;index:idx_record_dingtalk_reminder_due,priority:2;column:next_attempt_at"`
	LeaseUntil       *time.Time `gorm:"column:lease_until"`
	ClaimToken       string     `gorm:"size:64;not null;default:'';column:claim_token"`
	RequestedEventID *uint64    `gorm:"column:requested_event_id"`
	AttemptEventID   *uint64    `gorm:"column:attempt_event_id"`
	SentAt           *time.Time `gorm:"column:sent_at"`
	LastError        string     `gorm:"type:text;not null;default:'';column:last_error"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordDingTalkReminder) TableName() string {
	return "clinic_record_dingtalk_reminder"
}
