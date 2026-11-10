package model_test

import (
	"testing"

	roommodel "clinic-backend/internal/room/model"
)

func TestClinicCampus_CreateAndRetrieve(t *testing.T) {
	db := setupTestDB(t)

	campus := roommodel.ClinicCampus{Name: "Main Campus"}
	if err := db.Create(&campus).Error; err != nil {
		t.Fatalf("create campus: %v", err)
	}
	if campus.ID == 0 {
		t.Fatal("expected auto-incremented ID to be assigned")
	}

	var retrieved roommodel.ClinicCampus
	if err := db.First(&retrieved, campus.ID).Error; err != nil {
		t.Fatalf("retrieve campus: %v", err)
	}
	if retrieved.Name != campus.Name {
		t.Errorf("name = %q, want %q", retrieved.Name, campus.Name)
	}
}

func TestClinicCampus_NameIsUnique(t *testing.T) {
	db := setupTestDB(t)

	if err := db.Create(&roommodel.ClinicCampus{Name: "Main Campus"}).Error; err != nil {
		t.Fatalf("create first campus: %v", err)
	}
	if err := db.Create(&roommodel.ClinicCampus{Name: "Main Campus"}).Error; err == nil {
		t.Fatal("expected duplicate campus name to fail")
	}
}

func TestClinicCampus_TableName(t *testing.T) {
	if got := (roommodel.ClinicCampus{}).TableName(); got != "clinic_campus" {
		t.Errorf("table name = %q, want %q", got, "clinic_campus")
	}
}
