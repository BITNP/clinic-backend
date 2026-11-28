package model

import "time"

const (
	ReminderKindStart      = "start"
	ReminderKindCompletion = "completion"
)

// ServiceDateReminder persists one delivery attempt stream for one recipient.
type ServiceDateReminder struct {
	ID                uint       `gorm:"primaryKey"`
	ServiceDateID     uint       `gorm:"not null;uniqueIndex:idx_reminder_identity,priority:1"`
	ServiceStartAt    time.Time  `gorm:"not null;uniqueIndex:idx_reminder_identity,priority:2"`
	LeadTimeSeconds   int64      `gorm:"not null;uniqueIndex:idx_reminder_identity,priority:3"`
	StaffID           int        `gorm:"not null;uniqueIndex:idx_reminder_identity,priority:4"`
	Kind              string     `gorm:"size:16;not null;default:'start';uniqueIndex:idx_reminder_identity,priority:5"`
	Status            string     `gorm:"size:16;not null;index:idx_reminder_due,priority:1"`
	Attempts          int        `gorm:"not null;default:0"`
	NextAttemptAt     time.Time  `gorm:"not null;index:idx_reminder_due,priority:2"`
	LeaseUntil        *time.Time `gorm:"index"`
	ClaimToken        string     `gorm:"size:64;not null;default:''"`
	GatewayDeliveryID string     `gorm:"size:128;not null;default:''"`
	GatewayAccount    string     `gorm:"size:64;not null;default:''"`
	AcceptedAt        *time.Time
	SentAt            *time.Time
	LastTrackedAt     *time.Time
	LastError         string `gorm:"type:text;not null;default:''"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (ServiceDateReminder) TableName() string { return "clinic_service_date_reminder" }
