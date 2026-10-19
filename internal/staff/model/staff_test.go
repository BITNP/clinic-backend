package model_test

import (
	"testing"

	staffmodel "clinic-backend/internal/staff/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupStaffTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}

	if err := db.AutoMigrate(&staffmodel.ClinicStaff{}, &staffmodel.ClinicStaffWorkyear{}); err != nil {
		t.Fatalf("failed to migrate staff models: %v", err)
	}

	return db
}

func TestClinicStaff_CreateAndRetrieve(t *testing.T) {
	db := setupStaffTestDB(t)

	staff := staffmodel.ClinicStaff{
		ID:        42,
		AccountID: "cas:student42",
		Realname:  "Alice Smith",
		Email:     "alice@example.com",
	}

	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}

	var retrieved staffmodel.ClinicStaff
	if err := db.First(&retrieved, staff.ID).Error; err != nil {
		t.Fatalf("failed to retrieve staff: %v", err)
	}

	if retrieved.AccountID != staff.AccountID {
		t.Errorf("expected account_id %q, got %q", staff.AccountID, retrieved.AccountID)
	}
	if retrieved.Realname != staff.Realname {
		t.Errorf("expected realname %q, got %q", staff.Realname, retrieved.Realname)
	}
	if retrieved.Email != staff.Email {
		t.Errorf("expected email %q, got %q", staff.Email, retrieved.Email)
	}
	if retrieved.Version != 0 {
		t.Errorf("expected default version 0, got %d", retrieved.Version)
	}
}

func TestClinicStaff_AccountIDUnique(t *testing.T) {
	db := setupStaffTestDB(t)

	if err := db.Create(&staffmodel.ClinicStaff{ID: 1, AccountID: "duplicate"}).Error; err != nil {
		t.Fatalf("failed to create first staff: %v", err)
	}

	if err := db.Create(&staffmodel.ClinicStaff{ID: 2, AccountID: "duplicate"}).Error; err == nil {
		t.Fatal("expected duplicate account_id to fail")
	}
}

func TestClinicStaffWorkyear_CreateAndRetrieve(t *testing.T) {
	db := setupStaffTestDB(t)

	staff := staffmodel.ClinicStaff{ID: 7, AccountID: "cas:staff7"}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}

	workyear := staffmodel.ClinicStaffWorkyear{StaffID: staff.ID, WorkYear: 2026}
	if err := db.Create(&workyear).Error; err != nil {
		t.Fatalf("failed to create workyear: %v", err)
	}

	var retrieved staffmodel.ClinicStaffWorkyear
	if err := db.First(&retrieved, []interface{}{workyear.StaffID, workyear.WorkYear}).Error; err != nil {
		t.Fatalf("failed to retrieve workyear: %v", err)
	}

	if retrieved.StaffID != workyear.StaffID {
		t.Errorf("expected staff_id %d, got %d", workyear.StaffID, retrieved.StaffID)
	}
	if retrieved.WorkYear != workyear.WorkYear {
		t.Errorf("expected work_year %d, got %d", workyear.WorkYear, retrieved.WorkYear)
	}
}

func TestClinicStaffWorkyear_CompositeKeyPreventsDuplicate(t *testing.T) {
	db := setupStaffTestDB(t)

	staff := staffmodel.ClinicStaff{ID: 8, AccountID: "cas:staff8"}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}

	workyear := staffmodel.ClinicStaffWorkyear{StaffID: staff.ID, WorkYear: 2025}
	if err := db.Create(&workyear).Error; err != nil {
		t.Fatalf("failed to create first workyear: %v", err)
	}

	if err := db.Create(&staffmodel.ClinicStaffWorkyear{StaffID: staff.ID, WorkYear: 2025}).Error; err == nil {
		t.Fatal("expected duplicate composite key to fail")
	}
}
