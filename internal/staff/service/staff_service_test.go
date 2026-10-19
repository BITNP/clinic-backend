package service_test

import (
	"reflect"
	"testing"

	staffmodel "clinic-backend/internal/staff/model"
	staffservice "clinic-backend/internal/staff/service"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestStaffService_EnsureWorkYears(t *testing.T) {
	db := setupStaffTestDB(t)
	svc := staffservice.NewStaffService(db)

	staff := staffmodel.ClinicStaff{ID: 9, AccountID: "cas:staff9"}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}

	if err := svc.EnsureWorkYears(staff.ID, []int{2025, 2026, 2025}); err != nil {
		t.Fatalf("ensure work years: %v", err)
	}

	var years []staffmodel.ClinicStaffWorkyear
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

	var years2 []staffmodel.ClinicStaffWorkyear
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
