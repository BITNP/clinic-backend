package model_test

import (
	"testing"

	configmodel "clinic-backend/internal/config/model"
)

func TestClinicConfigGroup_CreateAndRetrieve(t *testing.T) {
	db := setupConfigModelTestDB(t)

	group := configmodel.ClinicConfigGroup{
		Name:        "booking",
		DisplayName: "Booking Settings",
		SortOrder:   2,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create config group: %v", err)
	}
	if group.ID == 0 {
		t.Fatal("expected auto-incremented ID to be assigned")
	}

	var retrieved configmodel.ClinicConfigGroup
	if err := db.First(&retrieved, group.ID).Error; err != nil {
		t.Fatalf("retrieve config group: %v", err)
	}
	if retrieved.Name != group.Name {
		t.Errorf("name = %q, want %q", retrieved.Name, group.Name)
	}
	if retrieved.DisplayName != group.DisplayName {
		t.Errorf("display_name = %q, want %q", retrieved.DisplayName, group.DisplayName)
	}
	if retrieved.SortOrder != group.SortOrder {
		t.Errorf("sort_order = %d, want %d", retrieved.SortOrder, group.SortOrder)
	}
}

func TestClinicConfigGroup_NameIsUnique(t *testing.T) {
	db := setupConfigModelTestDB(t)

	if err := db.Create(&configmodel.ClinicConfigGroup{Name: "general", DisplayName: "General"}).Error; err != nil {
		t.Fatalf("create first config group: %v", err)
	}
	if err := db.Create(&configmodel.ClinicConfigGroup{Name: "general", DisplayName: "Duplicate"}).Error; err == nil {
		t.Fatal("expected duplicate config group name to fail")
	}
}

func TestClinicConfigGroup_NegativeSortOrderRejected(t *testing.T) {
	db := setupConfigModelTestDB(t)

	err := db.Create(&configmodel.ClinicConfigGroup{
		Name:        "invalid",
		DisplayName: "Invalid",
		SortOrder:   -1,
	}).Error
	if err == nil {
		t.Fatal("expected negative sort_order to be rejected by check constraint")
	}
}

func TestClinicConfigGroup_TableName(t *testing.T) {
	if got := (configmodel.ClinicConfigGroup{}).TableName(); got != "clinic_config_group" {
		t.Errorf("table name = %q, want %q", got, "clinic_config_group")
	}
}
