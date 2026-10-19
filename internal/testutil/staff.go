package testutil

import (
	"fmt"
	"testing"

	roommodel "clinic-backend/internal/room/model"
	staffmodel "clinic-backend/internal/staff/model"

	"gorm.io/gorm"
)

func SeedRoom(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	r := roommodel.ClinicRoom{Name: name, Address: "addr"}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	return r.ID
}

func SeedOneStaff(t *testing.T, db *gorm.DB) int {
	t.Helper()
	s := staffmodel.ClinicStaff{AccountID: "test-staff"}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed staff: %v", err)
	}
	return s.ID
}

func SeedStaffWorkYear(t *testing.T, db *gorm.DB, staffID int, year int) {
	t.Helper()
	if err := db.Create(&staffmodel.ClinicStaffWorkyear{StaffID: staffID, WorkYear: year}).Error; err != nil {
		t.Fatalf("seed staff work year: %v", err)
	}
}

func SeedStaff(t *testing.T, db *gorm.DB, n int) []int {
	ids := make([]int, n)
	for i := 0; i < n; i++ {
		s := staffmodel.ClinicStaff{AccountID: fmt.Sprintf("staff-%d", i)}
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("seed staff %d: %v", i, err)
		}
		ids[i] = s.ID
	}
	return ids
}
