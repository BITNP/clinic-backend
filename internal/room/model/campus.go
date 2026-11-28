package model

// NoShowBanLevel determines which ticket scope receives an automatic no-show ban.
type NoShowBanLevel string

const (
	NoShowBanLevelCampus NoShowBanLevel = "campus"
	NoShowBanLevelGlobal NoShowBanLevel = "global"

	DefaultNoShowBanDurationHours int64 = 48
	MaxNoShowBanDurationHours     int64 = 2_562_047
)

// Valid reports whether the level is supported.
func (l NoShowBanLevel) Valid() bool {
	return l == NoShowBanLevelCampus || l == NoShowBanLevelGlobal
}

// NoShowBanPolicy configures automatic ticket bans after an appointment no-show.
type NoShowBanPolicy struct {
	Enabled       bool           `gorm:"not null;default:false" json:"enabled"`
	DurationHours int64          `gorm:"not null;default:48" json:"duration_hours"`
	Level         NoShowBanLevel `gorm:"type:varchar(10);not null;default:campus" json:"level"`
}

// DefaultNoShowBanPolicy returns the disabled policy applied to existing and
// newly created campuses when no custom values are supplied.
func DefaultNoShowBanPolicy() NoShowBanPolicy {
	return NoShowBanPolicy{
		Enabled:       false,
		DurationHours: DefaultNoShowBanDurationHours,
		Level:         NoShowBanLevelCampus,
	}
}

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
	NoShowBanPolicy NoShowBanPolicy `gorm:"embedded;embeddedPrefix:no_show_ban_" json:"no_show_ban_policy"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicCampus) TableName() string {
	return "clinic_campus"
}
