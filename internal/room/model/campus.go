package model

// SelectionPolicy determines how a campus recommends a room for booking.
type SelectionPolicy string

const (
	// SelectionPolicyBalanced spreads bookings by selecting the fewest tickets.
	SelectionPolicyBalanced SelectionPolicy = "balanced"
	// SelectionPolicyFulfill fills rooms in ascending ID order.
	SelectionPolicyFulfill SelectionPolicy = "fulfill"
)

// Valid reports whether the policy is supported.
func (p SelectionPolicy) Valid() bool {
	return p == SelectionPolicyBalanced || p == SelectionPolicyFulfill
}

// ClinicCampus groups clinic rooms under a common campus.
type ClinicCampus struct {
	ID              uint            `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name            string          `gorm:"size:64;not null;uniqueIndex;column:name" json:"name"`
	SelectionPolicy SelectionPolicy `gorm:"type:varchar(20);not null;default:balanced;column:selection_policy" json:"selection_policy"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicCampus) TableName() string {
	return "clinic_campus"
}
