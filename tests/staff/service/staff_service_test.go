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

func TestStaffService_GetOrCreateByAccountID(t *testing.T) {
	db := setupStaffTestDB(t)
	svc := staffservice.NewStaffService(db)

	staff, err := svc.GetOrCreateByAccountID("cas:staff10", "张三", "zhangsan@example.com", "123456789")
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if staff.QQNumber != "123456789" {
		t.Errorf("qq number after create: got %q, want %q", staff.QQNumber, "123456789")
	}

	// A later login may omit realname and email while changing the QQ number.
	staff, err = svc.GetOrCreateByAccountID("cas:staff10", "", "", "987654321")
	if err != nil {
		t.Fatalf("update staff: %v", err)
	}
	if staff.QQNumber != "987654321" {
		t.Errorf("qq number after update: got %q, want %q", staff.QQNumber, "987654321")
	}

	var stored staffmodel.ClinicStaff
	if err := db.Where("account_id = ?", "cas:staff10").First(&stored).Error; err != nil {
		t.Fatalf("load staff: %v", err)
	}
	if stored.Realname != "张三" {
		t.Errorf("realname should be preserved: got %q", stored.Realname)
	}
	if stored.Email != "zhangsan@example.com" {
		t.Errorf("email should be preserved: got %q", stored.Email)
	}
	if stored.QQNumber != "987654321" {
		t.Errorf("stored qq number: got %q, want %q", stored.QQNumber, "987654321")
	}

	// A missing claim must not clear the stored value.
	staff, err = svc.GetOrCreateByAccountID("cas:staff10", "", "", "")
	if err != nil {
		t.Fatalf("login without qq number: %v", err)
	}
	if staff.QQNumber != "987654321" {
		t.Errorf("empty qq number should not clear: got %q", staff.QQNumber)
	}

	manual := "555555"
	updated, err := svc.Update(staff.ID, staffservice.UpdateStaffInput{QQNumber: &manual})
	if err != nil {
		t.Fatalf("update qq number: %v", err)
	}
	if updated.QQNumber != manual {
		t.Errorf("qq number after manual update: got %q, want %q", updated.QQNumber, manual)
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
