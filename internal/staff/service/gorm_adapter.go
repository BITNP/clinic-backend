package service

import (
	"clinic-backend/internal/database"

	"gorm.io/gorm"
)

// NewStaffServiceFromGorm adapts callers that still expose a GORM handle while
// their own service is migrated to explicit SQL.
func NewStaffServiceFromGorm(db *gorm.DB) (*StaffService, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	return NewStaffService(database.Wrap(sqlDB, database.DriverSQLite)), nil
}
