package model

import (
	"time"

	roommodel "clinic-backend/internal/room/model"
)

// ClinicUserTicketBan blocks a user from creating clinic tickets until
// ExpirateAt. A nil CampusID applies the ban to every campus.
type ClinicUserTicketBan struct {
	ID         uint                    `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	UserID     string                  `gorm:"size:50;not null;index:idx_user_ticket_ban_user;column:user_id" json:"user_id"`
	CampusID   *uint                   `gorm:"column:campus_id;index:idx_user_ticket_ban_campus" json:"campus_id"`
	Campus     *roommodel.ClinicCampus `gorm:"foreignKey:CampusID;constraint:OnDelete:CASCADE" json:"-"`
	Reason     string                  `gorm:"size:500;not null;column:reason" json:"reason"`
	ExpirateAt time.Time               `gorm:"not null;index:idx_user_ticket_ban_expiry;column:expirate_at" json:"expirate_at"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicUserTicketBan) TableName() string {
	return "clinic_user_ticket_ban"
}
