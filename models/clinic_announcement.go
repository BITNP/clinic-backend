package models

import "time"

// AnnouncementTag represents the valid tag values for an announcement.
type AnnouncementTag string

const (
	AnnouncementTagNormal AnnouncementTag = "normal"
	AnnouncementTagPinned AnnouncementTag = "pinned"
	AnnouncementTagTOS    AnnouncementTag = "tos"
)

func (t AnnouncementTag) Valid() bool {
	switch t {
	case AnnouncementTagNormal, AnnouncementTagPinned, AnnouncementTagTOS:
		return true
	}
	return false
}

// ClinicAnnouncement represents a staff-published announcement or terms of service
// with an expiration date and a display priority.
type ClinicAnnouncement struct {
	ID             uint            `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Title          string          `gorm:"size:20;not null;column:title" json:"title"`
	Content        string          `gorm:"type:text;not null;column:content" json:"content"`
	Tag            AnnouncementTag `gorm:"size:16;not null;default:normal;column:tag" json:"tag"`
	CreatedTime    time.Time       `gorm:"not null;column:created_time" json:"createdTime"`
	LastEditedTime time.Time       `gorm:"not null;column:last_edited_time" json:"lastEditedTime"`
	ExpireDate     time.Time       `gorm:"type:date;not null;column:expire_date" json:"expireDate"`
	Priority       uint            `gorm:"not null;column:priority" json:"priority"`
	Brief          string          `gorm:"size:64;not null;column:brief" json:"brief"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicAnnouncement) TableName() string {
	return "clinic_announcement"
}
