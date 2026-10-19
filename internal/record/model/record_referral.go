package model

// ClinicRecordReferral records the reason a repair record was referred elsewhere.
type ClinicRecordReferral struct {
	RecordID       uint   `gorm:"primaryKey;not null;column:record_id" json:"record_id"`
	ReferralReason string `gorm:"size:600;not null;column:referral_reason" json:"referral_reason"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordReferral) TableName() string {
	return "clinic_record_referral"
}
