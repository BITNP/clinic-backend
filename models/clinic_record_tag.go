package models

// ClinicRecordTag marks a repair record with a label plus the rule used to
// apply that label. Tags are loaded into memory at startup.
type ClinicRecordTag struct {
	ID          uint   `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Title       string `gorm:"type:text;not null;uniqueIndex;column:title" json:"title"`
	Description string `gorm:"type:text;column:description" json:"description"`
	ApplyRule   string `gorm:"type:text;column:apply_rule" json:"apply_rule"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordTag) TableName() string {
	return "clinic_record_tag"
}
