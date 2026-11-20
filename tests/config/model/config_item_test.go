package model_test

import (
	"testing"

	configmodel "clinic-backend/internal/config/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupConfigModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fake database: %v", err)
	}
	if err := db.AutoMigrate(&configmodel.ClinicConfigGroup{}, &configmodel.ClinicConfigItem{}); err != nil {
		t.Fatalf("migrate config models: %v", err)
	}
	return db
}

func TestClinicConfigItem_CreateAndRetrieve(t *testing.T) {
	db := setupConfigModelTestDB(t)
	group := createConfigGroup(t, db, "booking")

	item := configmodel.ClinicConfigItem{
		Name:        "max_booking_days",
		Value:       "7",
		ValueType:   configmodel.ConfigValueTypeInt,
		DisplayName: "Maximum Booking Days",
		IsRequired:  true,
		GroupID:     group.ID,
		SortOrder:   3,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create config item: %v", err)
	}
	if item.ID == 0 {
		t.Fatal("expected auto-incremented ID to be assigned")
	}

	var retrieved configmodel.ClinicConfigItem
	if err := db.Preload("Group").First(&retrieved, item.ID).Error; err != nil {
		t.Fatalf("retrieve config item: %v", err)
	}
	if retrieved.Name != item.Name {
		t.Errorf("name = %q, want %q", retrieved.Name, item.Name)
	}
	if retrieved.Value != item.Value {
		t.Errorf("value = %q, want %q", retrieved.Value, item.Value)
	}
	if retrieved.ValueType != item.ValueType {
		t.Errorf("value_type = %q, want %q", retrieved.ValueType, item.ValueType)
	}
	if retrieved.IsRequired != item.IsRequired {
		t.Errorf("is_required = %t, want %t", retrieved.IsRequired, item.IsRequired)
	}
	if retrieved.Group == nil || retrieved.Group.ID != group.ID {
		t.Errorf("group = %+v, want group %d", retrieved.Group, group.ID)
	}
}

func TestClinicConfigItem_NameIsGloballyUnique(t *testing.T) {
	db := setupConfigModelTestDB(t)
	firstGroup := createConfigGroup(t, db, "first")
	secondGroup := createConfigGroup(t, db, "second")

	item := configmodel.ClinicConfigItem{
		Name:        "shared_name",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "First",
		GroupID:     firstGroup.ID,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create first config item: %v", err)
	}

	duplicate := configmodel.ClinicConfigItem{
		Name:        "shared_name",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Second",
		GroupID:     secondGroup.ID,
	}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("expected config item name to be unique across groups")
	}
}

func TestClinicConfigItem_RequiresExistingGroup(t *testing.T) {
	db := setupConfigModelTestDB(t)

	err := db.Create(&configmodel.ClinicConfigItem{
		Name:        "orphan",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Orphan",
		GroupID:     999,
	}).Error
	if err == nil {
		t.Fatal("expected missing group to be rejected by foreign key constraint")
	}
}

func TestClinicConfigItem_GroupDeletionRestricted(t *testing.T) {
	db := setupConfigModelTestDB(t)
	group := createConfigGroup(t, db, "protected")

	if err := db.Create(&configmodel.ClinicConfigItem{
		Name:        "item",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Item",
		GroupID:     group.ID,
	}).Error; err != nil {
		t.Fatalf("create config item: %v", err)
	}
	if err := db.Delete(&group).Error; err == nil {
		t.Fatal("expected group deletion to be restricted while it has items")
	}
}

func TestClinicConfigItem_NegativeSortOrderRejected(t *testing.T) {
	db := setupConfigModelTestDB(t)
	group := createConfigGroup(t, db, "ordering")

	err := db.Create(&configmodel.ClinicConfigItem{
		Name:        "invalid_order",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Invalid",
		GroupID:     group.ID,
		SortOrder:   -1,
	}).Error
	if err == nil {
		t.Fatal("expected negative sort_order to be rejected by check constraint")
	}
}

func TestClinicConfigItem_InvalidValueTypeRejected(t *testing.T) {
	db := setupConfigModelTestDB(t)
	group := createConfigGroup(t, db, "types")

	err := db.Create(&configmodel.ClinicConfigItem{
		Name:        "invalid_type",
		ValueType:   configmodel.ConfigValueType("date"),
		DisplayName: "Invalid",
		GroupID:     group.ID,
	}).Error
	if err == nil {
		t.Fatal("expected unsupported value_type to be rejected by check constraint")
	}
}

func TestConfigValueType_Valid(t *testing.T) {
	tests := []struct {
		valueType configmodel.ConfigValueType
		want      bool
	}{
		{valueType: configmodel.ConfigValueTypeInt, want: true},
		{valueType: configmodel.ConfigValueTypeDouble, want: true},
		{valueType: configmodel.ConfigValueTypeBoolean, want: true},
		{valueType: configmodel.ConfigValueTypeString, want: true},
		{valueType: configmodel.ConfigValueType("date"), want: false},
		{valueType: configmodel.ConfigValueType(""), want: false},
	}

	for _, tt := range tests {
		t.Run(string(tt.valueType), func(t *testing.T) {
			if got := tt.valueType.Valid(); got != tt.want {
				t.Errorf("Valid() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestClinicConfigItem_TableName(t *testing.T) {
	if got := (configmodel.ClinicConfigItem{}).TableName(); got != "clinic_config_item" {
		t.Errorf("table name = %q, want %q", got, "clinic_config_item")
	}
}

func createConfigGroup(t *testing.T, db *gorm.DB, name string) configmodel.ClinicConfigGroup {
	t.Helper()

	group := configmodel.ClinicConfigGroup{Name: name, DisplayName: name}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create config group %q: %v", name, err)
	}
	return group
}
