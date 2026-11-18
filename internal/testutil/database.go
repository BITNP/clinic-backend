package testutil

import (
	"testing"

	"clinic-backend/internal/database"

	"gorm.io/gorm"
)

// RawDB exposes the standard SQL handle under a GORM test fixture database.
func RawDB(t *testing.T, db *gorm.DB) *database.DB {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	driver := database.DriverSQLite
	if db.Dialector.Name() == string(database.DriverPostgres) {
		driver = database.DriverPostgres
	}
	return database.Wrap(sqlDB, driver)
}
