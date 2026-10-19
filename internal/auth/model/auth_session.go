package model

import (
	"time"
)

// AuthSession is a server-side session created after a successful CAS login.
// The browser only receives an opaque token; its SHA-256 hash is stored here.
type AuthSession struct {
	ID            int       `gorm:"primaryKey;column:id" json:"-"`
	TokenHash     string    `gorm:"type:text;not null;uniqueIndex;column:token_hash" json:"-"`
	StaffID       int       `gorm:"not null;column:staff_id" json:"-"`
	Role          string    `gorm:"size:16;not null;column:role" json:"-"`
	CSRFTokenHash string    `gorm:"type:text;not null;column:csrf_token_hash" json:"-"`
	CASTicket     string    `gorm:"type:text;column:cas_ticket" json:"-"`
	ExpiresAt     time.Time `gorm:"not null;column:expires_at" json:"-"`
	CreatedAt     time.Time `gorm:"column:created_at" json:"-"`
}

// TableName overrides GORM's default pluralized table name.
func (AuthSession) TableName() string {
	return "auth_session"
}
