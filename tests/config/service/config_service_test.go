package service_test

import (
	"errors"
	"testing"

	configmodel "clinic-backend/internal/config/model"
	configservice "clinic-backend/internal/config/service"
	"clinic-backend/internal/testutil"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupConfigServiceDB(t *testing.T) (*configservice.ConfigService, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fake database: %v", err)
	}
	if err := db.AutoMigrate(&configmodel.ClinicConfigGroup{}, &configmodel.ClinicConfigItem{}); err != nil {
		t.Fatalf("migrate config models: %v", err)
	}
	return configservice.NewConfigService(testutil.RawDB(t, db)), db
}

func TestConfigService_ListCatalogReturnsFlatOrderedData(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)

	booking, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "booking",
		DisplayName: "Booking",
		SortOrder:   2,
	})
	if err != nil {
		t.Fatalf("create booking group: %v", err)
	}
	general, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "general",
		DisplayName: "General",
		SortOrder:   1,
	})
	if err != nil {
		t.Fatalf("create general group: %v", err)
	}

	_, err = svc.CreateItem(configservice.CreateItemInput{
		Name:        "booking_title",
		Value:       "Reserve a slot",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Booking Title",
		GroupID:     booking.ID,
		SortOrder:   2,
	})
	if err != nil {
		t.Fatalf("create booking item: %v", err)
	}
	generalItem, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "enabled",
		Value:       "true",
		ValueType:   configmodel.ConfigValueTypeBoolean,
		DisplayName: "Enabled",
		IsRequired:  true,
		GroupID:     general.ID,
		SortOrder:   4,
	})
	if err != nil {
		t.Fatalf("create general item: %v", err)
	}

	catalog, err := svc.ListCatalog()
	if err != nil {
		t.Fatalf("list catalog: %v", err)
	}
	if len(catalog.Groups) != 2 {
		t.Fatalf("groups length = %d, want 2", len(catalog.Groups))
	}
	if catalog.Groups[0].Name != "general" || catalog.Groups[1].Name != "booking" {
		t.Errorf("group order = %q, %q; want general, booking", catalog.Groups[0].Name, catalog.Groups[1].Name)
	}
	if len(catalog.Items) != 2 {
		t.Fatalf("items length = %d, want 2", len(catalog.Items))
	}
	if catalog.Items[0].ID != generalItem.ID || catalog.Items[0].GroupID != general.ID {
		t.Errorf("first item = %+v, want general item", catalog.Items[0])
	}
	if catalog.Items[0].Group != nil {
		t.Errorf("catalog item contains nested group: %+v", catalog.Items[0].Group)
	}
}

func TestConfigService_GroupCRUD(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)

	group, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "general",
		DisplayName: "General",
		SortOrder:   1,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := svc.GetGroup(group.ID); err != nil {
		t.Fatalf("get group: %v", err)
	}

	displayName := "General Settings"
	sortOrder := 4
	updated, err := svc.UpdateGroup(group.ID, configservice.UpdateGroupInput{
		DisplayName: &displayName,
		SortOrder:   &sortOrder,
	})
	if err != nil {
		t.Fatalf("update group: %v", err)
	}
	if updated.DisplayName != displayName || updated.SortOrder != sortOrder {
		t.Errorf("updated group = %+v", updated)
	}

	if err := svc.DeleteGroup(group.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if _, err := svc.GetGroup(group.ID); !errors.Is(err, configservice.ErrConfigGroupNotFound) {
		t.Fatalf("expected group not found after delete, got %v", err)
	}
}

func TestConfigService_GroupNameUniqueAndDeleteInUse(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)

	group, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "general",
		DisplayName: "General",
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "general",
		DisplayName: "Duplicate",
	}); !errors.Is(err, configservice.ErrConfigGroupNameTaken) {
		t.Fatalf("expected duplicate group error, got %v", err)
	}

	if _, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "enabled",
		Value:       "true",
		ValueType:   configmodel.ConfigValueTypeBoolean,
		DisplayName: "Enabled",
		GroupID:     group.ID,
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}
	if err := svc.DeleteGroup(group.ID); !errors.Is(err, configservice.ErrConfigGroupInUse) {
		t.Fatalf("expected group-in-use error, got %v", err)
	}
}

func TestConfigService_ItemCRUDNormalizesValue(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)

	firstGroup := createGroup(t, svc, "first", 1)
	secondGroup := createGroup(t, svc, "second", 2)

	item, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "max_booking_days",
		Value:       " 007 ",
		ValueType:   configmodel.ConfigValueTypeInt,
		DisplayName: "Maximum Booking Days",
		IsRequired:  true,
		GroupID:     firstGroup.ID,
		SortOrder:   1,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if item.Value != "7" {
		t.Errorf("normalized value = %q, want 7", item.Value)
	}
	if item.RoleLevel != configmodel.ConfigRoleLevelAdmin || item.IsReadable {
		t.Errorf("access defaults = %q, %t; want admin, false", item.RoleLevel, item.IsReadable)
	}

	value := "false"
	valueType := configmodel.ConfigValueTypeBoolean
	displayName := "Enabled"
	required := false
	roleLevel := configmodel.ConfigRoleLevelStaff
	readable := true
	groupID := secondGroup.ID
	sortOrder := 3
	updated, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{
		Value:       &value,
		ValueType:   &valueType,
		DisplayName: &displayName,
		IsRequired:  &required,
		RoleLevel:   &roleLevel,
		IsReadable:  &readable,
		GroupID:     &groupID,
		SortOrder:   &sortOrder,
	})
	if err != nil {
		t.Fatalf("update item: %v", err)
	}
	if updated.Value != "false" || updated.ValueType != valueType || updated.DisplayName != displayName {
		t.Errorf("updated item = %+v", updated)
	}
	if updated.GroupID != groupID || updated.SortOrder != sortOrder || updated.IsRequired {
		t.Errorf("updated item relation/order = %+v", updated)
	}
	if updated.RoleLevel != roleLevel || !updated.IsReadable {
		t.Errorf("updated access = %q, %t; want %q, true", updated.RoleLevel, updated.IsReadable, roleLevel)
	}

	readable = false
	updated, err = svc.UpdateItem(item.ID, configservice.UpdateItemInput{IsReadable: &readable})
	if err != nil {
		t.Fatalf("disable reading: %v", err)
	}
	if updated.IsReadable {
		t.Error("is_readable = true after explicit false update")
	}

	if err := svc.DeleteItem(item.ID); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	if _, err := svc.GetItem(item.ID); !errors.Is(err, configservice.ErrConfigItemNotFound) {
		t.Fatalf("expected item not found after delete, got %v", err)
	}
}

func TestConfigService_ItemNameIsGloballyUnique(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)
	firstGroup := createGroup(t, svc, "first", 0)
	secondGroup := createGroup(t, svc, "second", 1)

	_, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "shared",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Shared",
		GroupID:     firstGroup.ID,
	})
	if err != nil {
		t.Fatalf("create first item: %v", err)
	}
	_, err = svc.CreateItem(configservice.CreateItemInput{
		Name:        "shared",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Duplicate",
		GroupID:     secondGroup.ID,
	})
	if !errors.Is(err, configservice.ErrConfigItemNameTaken) {
		t.Fatalf("expected duplicate item name error, got %v", err)
	}
}

func TestConfigService_ItemValidation(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)
	group := createGroup(t, svc, "general", 0)

	tests := []struct {
		name  string
		input configservice.CreateItemInput
		want  error
	}{
		{
			name: "unsupported type",
			input: configservice.CreateItemInput{
				Name: "bad_type", ValueType: "date", DisplayName: "Bad Type", GroupID: group.ID,
			},
			want: configservice.ErrConfigValueTypeInvalid,
		},
		{
			name: "invalid integer",
			input: configservice.CreateItemInput{
				Name: "bad_int", Value: "abc", ValueType: configmodel.ConfigValueTypeInt,
				DisplayName: "Bad Int", GroupID: group.ID,
			},
			want: configservice.ErrConfigValueInvalid,
		},
		{
			name: "invalid boolean",
			input: configservice.CreateItemInput{
				Name: "bad_boolean", Value: "yes", ValueType: configmodel.ConfigValueTypeBoolean,
				DisplayName: "Bad Boolean", GroupID: group.ID,
			},
			want: configservice.ErrConfigValueInvalid,
		},
		{
			name: "required empty",
			input: configservice.CreateItemInput{
				Name: "required_empty", Value: " ", ValueType: configmodel.ConfigValueTypeString,
				DisplayName: "Required Empty", IsRequired: true, GroupID: group.ID,
			},
			want: configservice.ErrConfigValueInvalid,
		},
		{
			name: "missing group",
			input: configservice.CreateItemInput{
				Name: "missing_group", ValueType: configmodel.ConfigValueTypeString,
				DisplayName: "Missing Group", GroupID: 999,
			},
			want: configservice.ErrConfigGroupNotFound,
		},
		{
			name: "invalid role level",
			input: configservice.CreateItemInput{
				Name: "invalid_role", ValueType: configmodel.ConfigValueTypeString,
				DisplayName: "Invalid Role", RoleLevel: configmodel.ConfigRoleLevel("guest"), GroupID: group.ID,
			},
			want: configservice.ErrConfigInvalidInput,
		},
		{
			name: "negative order",
			input: configservice.CreateItemInput{
				Name: "negative_order", ValueType: configmodel.ConfigValueTypeString,
				DisplayName: "Negative Order", GroupID: group.ID, SortOrder: -1,
			},
			want: configservice.ErrConfigInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateItem(tt.input)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CreateItem() error = %v, want %v", err, tt.want)
			}
		})
	}

	item, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "optional_empty",
		Value:       "",
		ValueType:   configmodel.ConfigValueTypeInt,
		DisplayName: "Optional Empty",
		GroupID:     group.ID,
	})
	if err != nil {
		t.Fatalf("create optional empty item: %v", err)
	}
	if item.Value != "" {
		t.Errorf("optional empty value = %q, want empty", item.Value)
	}
}

func createGroup(t *testing.T, svc *configservice.ConfigService, name string, sortOrder int) configmodel.ClinicConfigGroup {
	t.Helper()

	group, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        name,
		DisplayName: name,
		SortOrder:   sortOrder,
	})
	if err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}
	return group
}
