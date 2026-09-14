package models

// ClinicRecordTagPrompt stores the single general prompt that describes the
// clinic record tag set to the tagger. It is a singleton row loaded at startup.
type ClinicRecordTagPrompt struct {
	Singleton bool   `gorm:"primaryKey;column:singleton" json:"singleton"`
	Prompt    string `gorm:"type:text;not null;column:prompt" json:"prompt"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordTagPrompt) TableName() string {
	return "clinic_record_tag_prompt"
}
