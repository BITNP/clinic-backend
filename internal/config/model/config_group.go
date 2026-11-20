package model

// ClinicConfigGroup groups related configuration items for display.
type ClinicConfigGroup struct {
	ID          uint               `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name        string             `gorm:"size:64;not null;uniqueIndex;column:name" json:"name"`
	DisplayName string             `gorm:"size:128;not null;column:display_name" json:"display_name"`
	SortOrder   int                `gorm:"not null;default:0;column:sort_order;check:chk_config_group_sort_order,sort_order >= 0" json:"sort_order"`
	Items       []ClinicConfigItem `gorm:"foreignKey:GroupID" json:"items,omitempty"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicConfigGroup) TableName() string {
	return "clinic_config_group"
}
