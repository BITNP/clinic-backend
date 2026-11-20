package model

// ConfigValueType identifies how a configuration item's text value is interpreted.
type ConfigValueType string

const (
	ConfigValueTypeInt     ConfigValueType = "int"
	ConfigValueTypeDouble  ConfigValueType = "double"
	ConfigValueTypeBoolean ConfigValueType = "boolean"
	ConfigValueTypeString  ConfigValueType = "string"
)

// Valid reports whether the value type is supported.
func (t ConfigValueType) Valid() bool {
	switch t {
	case ConfigValueTypeInt, ConfigValueTypeDouble, ConfigValueTypeBoolean, ConfigValueTypeString:
		return true
	default:
		return false
	}
}

// ConfigRoleLevel identifies the staff role associated with a setting.
type ConfigRoleLevel string

const (
	ConfigRoleLevelStaff      ConfigRoleLevel = "staff"
	ConfigRoleLevelAdmin      ConfigRoleLevel = "admin"
	ConfigRoleLevelSuperAdmin ConfigRoleLevel = "superadmin"
)

// Valid reports whether the role level is supported.
func (l ConfigRoleLevel) Valid() bool {
	switch l {
	case ConfigRoleLevelStaff, ConfigRoleLevelAdmin, ConfigRoleLevelSuperAdmin:
		return true
	default:
		return false
	}
}

// ClinicConfigItem is a named value within a configuration group.
type ClinicConfigItem struct {
	ID          uint               `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name        string             `gorm:"size:128;not null;uniqueIndex;column:name" json:"name"`
	Value       string             `gorm:"type:text;not null;default:'';column:value" json:"value"`
	ValueType   ConfigValueType    `gorm:"type:varchar(16);not null;column:value_type;check:chk_config_item_value,sort_order >= 0 AND value_type IN ('int', 'double', 'boolean', 'string')" json:"value_type"`
	DisplayName string             `gorm:"size:128;not null;column:display_name" json:"display_name"`
	IsRequired  bool               `gorm:"not null;default:false;column:is_required" json:"is_required"`
	RoleLevel   ConfigRoleLevel    `gorm:"size:16;not null;default:admin;column:role_level;check:chk_config_item_role_level,role_level IN ('staff', 'admin', 'superadmin')" json:"role_level"`
	IsReadable  bool               `gorm:"not null;default:false;column:is_readable" json:"is_readable"`
	GroupID     uint               `gorm:"not null;index:idx_config_item_group_order,priority:1;column:group_id" json:"group_id"`
	Group       *ClinicConfigGroup `gorm:"foreignKey:GroupID;constraint:OnDelete:RESTRICT" json:"group,omitempty"`
	SortOrder   int                `gorm:"not null;default:0;index:idx_config_item_group_order,priority:2;column:sort_order" json:"sort_order"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicConfigItem) TableName() string {
	return "clinic_config_item"
}
