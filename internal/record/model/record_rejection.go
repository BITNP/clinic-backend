package model

// ClinicRecordRejection records the reason a repair record was rejected.
type ClinicRecordRejection struct {
	RecordID     uint   `gorm:"primaryKey;not null;column:record_id" json:"record_id"`
	RejectReason string `gorm:"size:600;not null;column:reject_reason" json:"reject_reason"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordRejection) TableName() string {
	return "clinic_record_rejection"
}
