package model

// ClinicRoom represents a physical clinic location where repair appointments are held.
type ClinicRoom struct {
	ID      uint   `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name    string `gorm:"size:64;not null;uniqueIndex;column:name" json:"name"`
	Address string `gorm:"size:256;not null;column:address" json:"address"`
	Enabled bool   `gorm:"not null;column:enabled" json:"enabled"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRoom) TableName() string {
	return "clinic_room"
}
