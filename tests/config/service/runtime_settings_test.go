package service_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	configmodel "clinic-backend/internal/config/model"
	configservice "clinic-backend/internal/config/service"
	schedulingmodel "clinic-backend/internal/scheduling/model"
)

func TestConcurrentRuntimeUpdatesPublishCommittedState(t *testing.T) {
	svc, db := setupConfigServiceDB(t)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatal(err)
	}
	if err := svc.InitializeRuntime(nil); err != nil {
		t.Fatal(err)
	}
	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "RECORD_PHOTO_MAX_COUNT").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			_, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value})
			if err != nil {
				errs <- err
			}
			_ = svc.Current()
		}(fmt.Sprint(i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	stored, err := svc.LoadRuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.RecordPhotoMaxCount != svc.Current().RecordPhotoMaxCount {
		t.Fatalf("stored=%d active=%d", stored.RecordPhotoMaxCount, svc.Current().RecordPhotoMaxCount)
	}
}

func TestRuntimeSettingsPublishOnlyAfterValidSave(t *testing.T) {
	svc, db := setupConfigServiceDB(t)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatal(err)
	}
	if err := svc.InitializeRuntime(func(s configservice.RuntimeSettings) error {
		if s.ServiceDateReminderEnabled {
			return fmt.Errorf("gateway unavailable")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "SERVICE_DATE_REMINDER_ENABLED").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	value := "true"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); !errors.Is(err, configservice.ErrConfigValueInvalid) {
		t.Fatalf("expected rejected save, got %v", err)
	}
	stored, err := svc.GetItem(item.ID)
	if err != nil || stored.Value != "false" || svc.Current().ServiceDateReminderEnabled {
		t.Fatalf("failed save changed state: item=%+v error=%v", stored, err)
	}
	if err := svc.DeleteItem(item.ID); !errors.Is(err, configservice.ErrConfigInvalidInput) {
		t.Fatalf("expected protected setting, got %v", err)
	}
	item = configmodel.ClinicConfigItem{}
	if err := db.Where("name = ?", "RECORD_PHOTO_MAX_COUNT").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	value = "4"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Current().RecordPhotoMaxCount; got != 4 {
		t.Fatalf("active photo limit = %d", got)
	}
	copy := svc.Current()
	copy.ServiceDateReminderLeadTimes[0] = 0
	if svc.Current().ServiceDateReminderLeadTimes[0] == 0 {
		t.Fatal("getter exposed mutable snapshot")
	}
}

func TestRemovingLeadCancelsPendingJobsInSave(t *testing.T) {
	svc, db := setupConfigServiceDB(t)
	if err := db.AutoMigrate(&schedulingmodel.ServiceDateReminder{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatal(err)
	}
	if err := svc.InitializeRuntime(nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := schedulingmodel.ServiceDateReminder{ServiceDateID: 1, ServiceStartAt: now.Add(time.Hour), LeadTimeSeconds: int64((5 * time.Hour) / time.Second), StaffID: 1, Status: "pending", NextAttemptAt: now}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "SERVICE_DATE_REMINDER_LEAD_TIMES").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	value := "3h"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); err != nil {
		t.Fatal(err)
	}
	value = "5h,3h"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&job, job.ID).Error; err != nil || job.Status != "cancelled" {
		t.Fatalf("job after removing and restoring lead: %+v, %v", job, err)
	}
}

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
	if settings.TaggerLLMAPIKey != "" {
		t.Errorf("TaggerLLMAPIKey = %q, want empty", settings.TaggerLLMAPIKey)
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
	if groupCount != 3 || itemCount != 7 {
		t.Errorf("seeded counts = %d groups, %d items; want 3 and 7", groupCount, itemCount)
	}

	var taggerKey configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_LLM_API_KEY").First(&taggerKey).Error; err != nil {
		t.Fatalf("get tagger API key item: %v", err)
	}
	if taggerKey.Value != "" || !taggerKey.WriteOnly || taggerKey.IsRequired {
		t.Errorf("tagger API key item = %+v", taggerKey)
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
	t.Setenv("TAGGER_LLM_API_KEY", "ignored-environment-key")

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
		settings.ServiceDateReminderEnabled ||
		settings.TaggerLLMAPIKey != "" {
		t.Errorf("JSON defaults = %+v", settings)
	}
	if len(settings.ServiceDateReminderLeadTimes) != 2 ||
		settings.ServiceDateReminderLeadTimes[0] != 5*time.Hour ||
		settings.ServiceDateReminderLeadTimes[1] != 3*time.Hour {
		t.Errorf("JSON lead times = %v, want [5h 3h]", settings.ServiceDateReminderLeadTimes)
	}
}

func TestConfigService_EnsuresTaggerAPIKeyFromLegacyEnvironmentOnce(t *testing.T) {
	svc, db := setupConfigServiceDB(t)
	if err := svc.EnsureRuntimeSettingsWithInitialTaggerAPIKey("legacy-key"); err != nil {
		t.Fatalf("ensure runtime settings: %v", err)
	}

	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_LLM_API_KEY").First(&item).Error; err != nil {
		t.Fatalf("get tagger API key item: %v", err)
	}
	if item.Value != "legacy-key" || !item.WriteOnly || item.IsRequired {
		t.Errorf("seeded tagger API key item = %+v", item)
	}

	settings, err := svc.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load runtime settings: %v", err)
	}
	if settings.TaggerLLMAPIKey != "legacy-key" {
		t.Errorf("TaggerLLMAPIKey = %q, want legacy-key", settings.TaggerLLMAPIKey)
	}

	if err := svc.EnsureRuntimeSettingsWithInitialTaggerAPIKey("replacement-key"); err != nil {
		t.Fatalf("ensure runtime settings second time: %v", err)
	}
	if err := db.Where("name = ?", "TAGGER_LLM_API_KEY").First(&item).Error; err != nil {
		t.Fatalf("get tagger API key item after reseed: %v", err)
	}
	if item.Value != "legacy-key" {
		t.Errorf("tagger API key = %q, want existing legacy-key", item.Value)
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

	taggerKey, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "TAGGER_LLM_API_KEY",
		Value:       "provider-key",
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: "Tagger API Key",
		WriteOnly:   true,
		GroupID:     group.ID,
	})
	if err != nil {
		t.Fatalf("create tagger API key setting: %v", err)
	}
	required = true
	if _, err := svc.UpdateItem(taggerKey.ID, configservice.UpdateItemInput{IsRequired: &required}); !errors.Is(err, configservice.ErrConfigInvalidInput) {
		t.Fatalf("expected optional tagger key error, got %v", err)
	}
	writeOnly := false
	if _, err := svc.UpdateItem(taggerKey.ID, configservice.UpdateItemInput{WriteOnly: &writeOnly}); !errors.Is(err, configservice.ErrConfigInvalidInput) {
		t.Fatalf("expected write-only tagger key error, got %v", err)
	}
}
