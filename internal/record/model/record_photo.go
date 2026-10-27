package model

import "time"

// ClinicRecordPhoto is one ordered photo attached to a record's worker notes.
// The image itself lives in S3; only its object key and metadata are stored
// here.
type ClinicRecordPhoto struct {
	ID          uint      `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	RecordID    uint      `gorm:"not null;index:idx_record_photo_position,priority:1;column:record_id" json:"record_id"`
	ObjectKey   string    `gorm:"size:512;not null;column:object_key" json:"-"`
	ContentType string    `gorm:"size:100;not null;column:content_type" json:"content_type"`
	Size        int64     `gorm:"not null;column:size" json:"size"`
	Position    int       `gorm:"not null;index:idx_record_photo_position,priority:2;column:position" json:"position"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordPhoto) TableName() string {
	return "clinic_record_photo"
}
