package model

// ClinicStaffWorkyear records which calendar years a staff member has worked.
type ClinicStaffWorkyear struct {
	StaffID  int `gorm:"primaryKey;not null;column:staff_id" json:"staff_id"`
	WorkYear int `gorm:"primaryKey;not null;column:work_year" json:"work_year"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicStaffWorkyear) TableName() string {
	return "clinic_staff_workyear"
}
