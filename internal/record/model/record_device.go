package model

// ClinicRecordDevice holds optional device information for a repair record.
type ClinicRecordDevice struct {
	RecordID    uint   `gorm:"primaryKey;not null;column:record_id" json:"record_id"`
	LaptopModel string `gorm:"size:200;not null;column:laptop_model" json:"laptop_model"`
	Password    string `gorm:"size:256;column:password" json:"password"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordDevice) TableName() string {
	return "clinic_record_device"
}
