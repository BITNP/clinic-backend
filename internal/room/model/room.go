package model

// ClinicRoom represents a physical clinic location where repair appointments are held.
type ClinicRoom struct {
	ID       uint          `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name     string        `gorm:"size:64;not null;uniqueIndex;column:name" json:"name"`
	Address  string        `gorm:"size:256;not null;column:address" json:"address"`
	Enabled  bool          `gorm:"not null;column:enabled" json:"enabled"`
	CampusID *uint         `gorm:"column:campus_id;index" json:"campus_id"`
	Campus   *ClinicCampus `gorm:"foreignKey:CampusID;constraint:OnDelete:RESTRICT" json:"campus,omitempty"`
	Type     RoomType      `gorm:"type:varchar(20);not null;default:normal;column:type" json:"type"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRoom) TableName() string {
	return "clinic_room"
}
