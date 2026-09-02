package tests

import (
	"reflect"
	"testing"

	"clinic-backend/models"
	"clinic-backend/services"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupStaffTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}

	if err := db.AutoMigrate(&models.ClinicStaff{}, &models.ClinicStaffWorkyear{}); err != nil {
		t.Fatalf("failed to migrate staff models: %v", err)
	}

	return db
}

func TestClinicStaff_CreateAndRetrieve(t *testing.T) {
	db := setupStaffTestDB(t)

	staff := models.ClinicStaff{
		ID:        42,
		AccountID: "cas:student42",
		Realname:  "Alice Smith",
		Email:     "alice@example.com",
	}

	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}

	var retrieved models.ClinicStaff
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
}

func TestClinicStaff_AccountIDUnique(t *testing.T) {
	db := setupStaffTestDB(t)

	if err := db.Create(&models.ClinicStaff{ID: 1, AccountID: "duplicate"}).Error; err != nil {
		t.Fatalf("failed to create first staff: %v", err)
	}

	if err := db.Create(&models.ClinicStaff{ID: 2, AccountID: "duplicate"}).Error; err == nil {
		t.Fatal("expected duplicate account_id to fail")
	}
}

func TestClinicStaffWorkyear_CreateAndRetrieve(t *testing.T) {
	db := setupStaffTestDB(t)

	staff := models.ClinicStaff{ID: 7, AccountID: "cas:staff7"}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}

	workyear := models.ClinicStaffWorkyear{StaffID: staff.ID, WorkYear: 2026}
	if err := db.Create(&workyear).Error; err != nil {
		t.Fatalf("failed to create workyear: %v", err)
	}

	var retrieved models.ClinicStaffWorkyear
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

	staff := models.ClinicStaff{ID: 8, AccountID: "cas:staff8"}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}

	workyear := models.ClinicStaffWorkyear{StaffID: staff.ID, WorkYear: 2025}
	if err := db.Create(&workyear).Error; err != nil {
		t.Fatalf("failed to create first workyear: %v", err)
	}

	if err := db.Create(&models.ClinicStaffWorkyear{StaffID: staff.ID, WorkYear: 2025}).Error; err == nil {
		t.Fatal("expected duplicate composite key to fail")
	}
}

func TestStaffService_EnsureWorkYears(t *testing.T) {
	db := setupStaffTestDB(t)
	svc := services.NewStaffService(db)

	staff := models.ClinicStaff{ID: 9, AccountID: "cas:staff9"}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}

	if err := svc.EnsureWorkYears(staff.ID, []int{2025, 2026, 2025}); err != nil {
		t.Fatalf("ensure work years: %v", err)
	}

	var years []models.ClinicStaffWorkyear
	if err := db.Where("staff_id = ?", staff.ID).Order("work_year ASC").Find(&years).Error; err != nil {
		t.Fatalf("load work years: %v", err)
	}
	got := make([]int, len(years))
	for i, y := range years {
		got[i] = y.WorkYear
	}
	if !reflect.DeepEqual(got, []int{2025, 2026}) {
		t.Errorf("work years after first ensure: got %v, want [2025 2026]", got)
	}

	if err := svc.EnsureWorkYears(staff.ID, []int{2026, 2027}); err != nil {
		t.Fatalf("ensure work years again: %v", err)
	}

	var years2 []models.ClinicStaffWorkyear
	if err := db.Where("staff_id = ?", staff.ID).Order("work_year ASC").Find(&years2).Error; err != nil {
		t.Fatalf("load work years: %v", err)
	}
	got2 := make([]int, len(years2))
	for i, y := range years2 {
		got2[i] = y.WorkYear
	}
	if !reflect.DeepEqual(got2, []int{2025, 2026, 2027}) {
		t.Errorf("work years after second ensure: got %v, want [2025 2026 2027]", got2)
	}
}
