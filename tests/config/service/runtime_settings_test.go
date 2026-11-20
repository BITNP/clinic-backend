package service_test

import (
	"errors"
	"testing"
	"time"

	configmodel "clinic-backend/internal/config/model"
	configservice "clinic-backend/internal/config/service"
)

func TestConfigService_EnsureRuntimeSettingsSeedsDefaults(t *testing.T) {
	svc, db := setupConfigServiceDB(t)

	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings: %v", err)
	}
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings second time: %v", err)
	}

	settings, err := svc.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load runtime settings: %v", err)
	}
	if settings.SyncPollInterval != 10*time.Second {
		t.Errorf("SyncPollInterval = %v, want 10s", settings.SyncPollInterval)
	}
	if settings.StaffVersion != 0 {
		t.Errorf("StaffVersion = %d, want 0", settings.StaffVersion)
	}
	if settings.RecordActionWindow != 5*time.Minute {
		t.Errorf("RecordActionWindow = %v, want 5m", settings.RecordActionWindow)
	}
	if settings.RecordPhotoMaxCount != 9 {
		t.Errorf("RecordPhotoMaxCount = %d, want 9", settings.RecordPhotoMaxCount)
	}
	if settings.ServiceDateReminderEnabled {
		t.Error("ServiceDateReminderEnabled = true, want false")
	}
	if len(settings.ServiceDateReminderLeadTimes) != 2 ||
		settings.ServiceDateReminderLeadTimes[0] != 5*time.Hour ||
		settings.ServiceDateReminderLeadTimes[1] != 3*time.Hour {
		t.Errorf("ServiceDateReminderLeadTimes = %v, want [5h 3h]", settings.ServiceDateReminderLeadTimes)
	}

	var groupCount, itemCount int64
	if err := db.Model(&configmodel.ClinicConfigGroup{}).Count(&groupCount).Error; err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if err := db.Model(&configmodel.ClinicConfigItem{}).Count(&itemCount).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if groupCount != 3 || itemCount != 6 {
		t.Errorf("seeded counts = %d groups, %d items; want 3 and 6", groupCount, itemCount)
	}

	var maintainerCount int64
	if err := db.Model(&configmodel.ClinicConfigItem{}).
		Where("name = ?", "MAINTAINER_QQ_IDS").
		Count(&maintainerCount).Error; err != nil {
		t.Fatalf("count maintainer setting: %v", err)
	}
	if maintainerCount != 0 {
		t.Errorf("MAINTAINER_QQ_IDS should not be seeded")
	}
}

func TestConfigService_EnsureRuntimeSettingsUsesJSONAndIgnoresEnvironment(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)

	t.Setenv("SYNC_POLL_INTERVAL", "20s")
	t.Setenv("STAFF_VERSION", "4")
	t.Setenv("RECORD_ACTION_WINDOW", "2m")
	t.Setenv("RECORD_PHOTO_MAX_COUNT", "5")
	t.Setenv("SERVICE_DATE_REMINDER_ENABLED", "true")
	t.Setenv("SERVICE_DATE_REMINDER_LEAD_TIMES", "3h,7h")

	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings: %v", err)
	}

	settings, err := svc.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load runtime settings: %v", err)
	}
	if settings.SyncPollInterval != 10*time.Second ||
		settings.StaffVersion != 0 ||
		settings.RecordActionWindow != 5*time.Minute ||
		settings.RecordPhotoMaxCount != 9 ||
		settings.ServiceDateReminderEnabled {
		t.Errorf("JSON defaults = %+v", settings)
	}
	if len(settings.ServiceDateReminderLeadTimes) != 2 ||
		settings.ServiceDateReminderLeadTimes[0] != 5*time.Hour ||
		settings.ServiceDateReminderLeadTimes[1] != 3*time.Hour {
		t.Errorf("JSON lead times = %v, want [5h 3h]", settings.ServiceDateReminderLeadTimes)
	}
}

func TestConfigService_EnsureRuntimeSettingsPreservesConfiguredRows(t *testing.T) {
	svc, db := setupConfigServiceDB(t)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings: %v", err)
	}

	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "SYNC_POLL_INTERVAL").First(&item).Error; err != nil {
		t.Fatalf("get sync setting: %v", err)
	}
	value := "30s"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); err != nil {
		t.Fatalf("update sync setting: %v", err)
	}

	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings again: %v", err)
	}
	settings, err := svc.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load runtime settings: %v", err)
	}
	if settings.SyncPollInterval != 30*time.Second {
		t.Errorf("SyncPollInterval = %v, want 30s", settings.SyncPollInterval)
	}
}

func TestConfigService_LoadRuntimeSettingsRejectsInvalidStoredValue(t *testing.T) {
	svc, db := setupConfigServiceDB(t)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings: %v", err)
	}

	if err := db.Exec(
		"UPDATE clinic_config_item SET value = 'not-a-duration' WHERE name = $1",
		"RECORD_ACTION_WINDOW",
	).Error; err != nil {
		t.Fatalf("invalidate stored value: %v", err)
	}
	if _, err := svc.LoadRuntimeSettings(); !errors.Is(err, configservice.ErrConfigValueInvalid) {
		t.Fatalf("expected invalid stored value error, got %v", err)
	}
}

func TestConfigService_ValidatesKnownRuntimeSettings(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)
	group := createGroup(t, svc, "custom", 9)

	if _, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "RECORD_ACTION_WINDOW",
		Value:       "later",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Action Window",
		IsRequired:  true,
		GroupID:     group.ID,
	}); !errors.Is(err, configservice.ErrConfigValueInvalid) {
		t.Fatalf("expected invalid duration error, got %v", err)
	}

	item, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "SERVICE_DATE_REMINDER_LEAD_TIMES",
		Value:       "3h,7h",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Reminder Lead Times",
		IsRequired:  true,
		GroupID:     group.ID,
	})
	if err != nil {
		t.Fatalf("create known runtime setting: %v", err)
	}
	if item.Value != "7h,3h" {
		t.Errorf("lead times = %q, want 7h,3h", item.Value)
	}

	required := false
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{IsRequired: &required}); !errors.Is(err, configservice.ErrConfigInvalidInput) {
		t.Fatalf("expected required-setting error, got %v", err)
	}

	valueType := configmodel.ConfigValueTypeInt
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{ValueType: &valueType}); !errors.Is(err, configservice.ErrConfigValueTypeInvalid) {
		t.Fatalf("expected fixed value-type error, got %v", err)
	}
}
