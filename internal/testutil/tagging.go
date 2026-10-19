package testutil

import (
	"testing"

	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	taggingmodel "clinic-backend/internal/tagging/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func SetupTagTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// An in-memory SQLite database lives on a single connection. Background
	// workers must use the same connection as the test, otherwise they see a
	// different empty database.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := db.AutoMigrate(
		&roommodel.ClinicRoom{},
		&schedulingmodel.ClinicServiceDate{},
		&recordmodel.ClinicRecord{},
		&recordmodel.ClinicRecordDevice{},
		&taggingmodel.ClinicRecordTag{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
