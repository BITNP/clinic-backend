package model

// ClinicCampus groups clinic rooms under a common campus.
type ClinicCampus struct {
	ID   uint   `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name string `gorm:"size:64;not null;uniqueIndex;column:name" json:"name"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicCampus) TableName() string {
	return "clinic_campus"
}
