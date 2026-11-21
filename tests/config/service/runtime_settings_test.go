package service_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	configmodel "clinic-backend/internal/config/model"
	configservice "clinic-backend/internal/config/service"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	"clinic-backend/internal/testutil"
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

func TestRuntimeSettingsApplyHookRunsAfterValidSave(t *testing.T) {
	svc, db := setupConfigServiceDB(t)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatal(err)
	}
	if err := svc.InitializeRuntime(nil); err != nil {
		t.Fatal(err)
	}

	var applied configservice.RuntimeSettings
	calls := 0
	svc.SetRuntimeApplyHook(func(settings configservice.RuntimeSettings) {
		calls++
		applied = settings
	})

	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_AGENT_POOL_SIZE").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	value := "6"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || applied.TaggerAgentPoolSize != 6 {
		t.Fatalf("apply hook calls=%d settings=%+v", calls, applied)
	}

	value = "0"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); !errors.Is(err, configservice.ErrConfigValueInvalid) {
		t.Fatalf("expected invalid pool size, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("apply hook ran for rejected update: calls=%d", calls)
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
	if err := db.Model(&configmodel.ClinicConfigItem{}).
		Where("name = ?", "TAGGER_AGENT_POOL_SIZE").
		Updates(map[string]any{
			"unit":       "stale",
			"hint":       "stale",
			"role_level": configmodel.ConfigRoleLevelAdmin,
		}).Error; err != nil {
		t.Fatalf("corrupt reserved metadata: %v", err)
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
	if settings.RecordPhotoMaxSizeBytes != 10485760 {
		t.Errorf("RecordPhotoMaxSizeBytes = %d, want 10485760", settings.RecordPhotoMaxSizeBytes)
	}
	if settings.ServiceDateReminderEnabled {
		t.Error("ServiceDateReminderEnabled = true, want false")
	}
	if settings.MessageGatewayAccount != "qq" {
		t.Errorf("MessageGatewayAccount = %q, want qq", settings.MessageGatewayAccount)
	}
	if settings.MessageGatewayURL != "http://message-gateway:8081" {
		t.Errorf("MessageGatewayURL = %q", settings.MessageGatewayURL)
	}
	if settings.MessageGatewayAccountToken != "" {
		t.Errorf("MessageGatewayAccountToken = %q, want empty", settings.MessageGatewayAccountToken)
	}
	if settings.MessageGatewayTimeout != 5*time.Second {
		t.Errorf("MessageGatewayTimeout = %v, want 5s", settings.MessageGatewayTimeout)
	}
	if settings.ServiceDateReminderTemplate != configservice.DefaultServiceDateReminderTemplate {
		t.Errorf("ServiceDateReminderTemplate = %q", settings.ServiceDateReminderTemplate)
	}
	if settings.TaggerLLMAPIKey != "" {
		t.Errorf("TaggerLLMAPIKey = %q, want empty", settings.TaggerLLMAPIKey)
	}
	if settings.TaggerLLMBaseURL != "" || settings.TaggerLLMModel != "" {
		t.Errorf("tagger endpoint defaults = %q, %q; want empty", settings.TaggerLLMBaseURL, settings.TaggerLLMModel)
	}
	if settings.TaggerAgentPoolSize != 4 || settings.TaggerContextTokens != 32000 || settings.TaggerTimeout != 30*time.Second {
		t.Errorf(
			"tagger runtime defaults = size %d, context %d, timeout %v",
			settings.TaggerAgentPoolSize,
			settings.TaggerContextTokens,
			settings.TaggerTimeout,
		)
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
	if groupCount != 3 || itemCount != 18 {
		t.Errorf("seeded counts = %d groups, %d items; want 3 and 18", groupCount, itemCount)
	}

	var taggerKey configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_LLM_API_KEY").First(&taggerKey).Error; err != nil {
		t.Fatalf("get tagger API key item: %v", err)
	}
	if taggerKey.Value != "" || !taggerKey.WriteOnly || taggerKey.IsRequired {
		t.Errorf("tagger API key item = %+v", taggerKey)
	}
	var gatewayToken configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "MESSAGE_GATEWAY_ACCOUNT_TOKEN").First(&gatewayToken).Error; err != nil {
		t.Fatalf("get gateway token item: %v", err)
	}
	if gatewayToken.Value != "" || !gatewayToken.WriteOnly || gatewayToken.IsRequired {
		t.Errorf("gateway token item = %+v", gatewayToken)
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

	var poolSize configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_AGENT_POOL_SIZE").First(&poolSize).Error; err != nil {
		t.Fatalf("get tagger pool size item: %v", err)
	}
	if poolSize.Unit != "并发" || poolSize.Hint == "" {
		t.Errorf("tagger pool presentation metadata = %q, %q", poolSize.Unit, poolSize.Hint)
	}
	var contextTokens configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_CONTEXT_TOKENS").First(&contextTokens).Error; err != nil {
		t.Fatalf("get tagger context tokens item: %v", err)
	}
	if contextTokens.Unit != "tokens" || contextTokens.Hint == "" {
		t.Errorf("tagger context presentation metadata = %q, %q", contextTokens.Unit, contextTokens.Hint)
	}
	for _, name := range []string{
		"TAGGER_LLM_API_KEY",
		"TAGGER_LLM_BASE_URL",
		"TAGGER_LLM_MODEL",
		"TAGGER_AGENT_POOL_SIZE",
		"TAGGER_CONTEXT_TOKENS",
		"TAGGER_TIMEOUT",
		"MESSAGE_GATEWAY_URL",
		"MESSAGE_GATEWAY_ACCOUNT",
		"MESSAGE_GATEWAY_ACCOUNT_TOKEN",
		"MESSAGE_GATEWAY_TIMEOUT",
	} {
		t.Run(name, func(t *testing.T) {
			var item configmodel.ClinicConfigItem
			if err := db.Where("name = ?", name).First(&item).Error; err != nil {
				t.Fatalf("get superadmin-only item: %v", err)
			}
			if item.RoleLevel != configmodel.ConfigRoleLevelSuperAdmin {
				t.Errorf("role_level = %q, want superadmin", item.RoleLevel)
			}
		})
	}
	var syncPollInterval configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "SYNC_POLL_INTERVAL").First(&syncPollInterval).Error; err != nil {
		t.Fatalf("get admin runtime setting: %v", err)
	}
	if syncPollInterval.RoleLevel != configmodel.ConfigRoleLevelAdmin {
		t.Errorf("SYNC_POLL_INTERVAL role_level = %q, want admin", syncPollInterval.RoleLevel)
	}
}

func TestConfigService_EnsureRuntimeSettingsUsesExternalFileAndIgnoresEnvironment(t *testing.T) {
	svc, _ := setupConfigServiceDB(t)

	t.Setenv("SYNC_POLL_INTERVAL", "20s")
	t.Setenv("STAFF_VERSION", "4")
	t.Setenv("RECORD_ACTION_WINDOW", "2m")
	t.Setenv("RECORD_PHOTO_MAX_COUNT", "5")
	t.Setenv("RECORD_PHOTO_MAX_SIZE_BYTES", "2048")
	t.Setenv("SERVICE_DATE_REMINDER_ENABLED", "true")
	t.Setenv("SERVICE_DATE_REMINDER_LEAD_TIMES", "3h,7h")
	t.Setenv("SERVICE_DATE_REMINDER_TEMPLATE", "ignored template")
	t.Setenv("MESSAGE_GATEWAY_URL", "https://ignored.example")
	t.Setenv("MESSAGE_GATEWAY_ACCOUNT", "custom")
	t.Setenv("MESSAGE_GATEWAY_ACCOUNT_TOKEN", "ignored-token")
	t.Setenv("MESSAGE_GATEWAY_TIMEOUT", "9s")
	t.Setenv("TAGGER_LLM_API_KEY", "ignored-environment-key")
	t.Setenv("TAGGER_LLM_BASE_URL", "https://ignored.example/v1")
	t.Setenv("TAGGER_LLM_MODEL", "ignored-model")
	t.Setenv("TAGGER_AGENT_POOL_SIZE", "8")
	t.Setenv("TAGGER_CONTEXT_TOKENS", "64000")
	t.Setenv("TAGGER_TIMEOUT", "5s")

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
		settings.RecordPhotoMaxSizeBytes != 10485760 ||
		settings.ServiceDateReminderEnabled ||
		settings.MessageGatewayAccount != "qq" ||
		settings.MessageGatewayURL != "http://message-gateway:8081" ||
		settings.MessageGatewayAccountToken != "" ||
		settings.MessageGatewayTimeout != 5*time.Second ||
		settings.ServiceDateReminderTemplate != configservice.DefaultServiceDateReminderTemplate ||
		settings.TaggerLLMAPIKey != "" ||
		settings.TaggerAgentPoolSize != 4 ||
		settings.TaggerContextTokens != 32000 ||
		settings.TaggerTimeout != 30*time.Second {
		t.Errorf("JSON defaults = %+v", settings)
	}
	if len(settings.ServiceDateReminderLeadTimes) != 2 ||
		settings.ServiceDateReminderLeadTimes[0] != 5*time.Hour ||
		settings.ServiceDateReminderLeadTimes[1] != 3*time.Hour {
		t.Errorf("JSON lead times = %v, want [5h 3h]", settings.ServiceDateReminderLeadTimes)
	}
}

func TestConfigService_SeedsTaggerAPIKeyFromExternalFile(t *testing.T) {
	_, db := setupConfigServiceDB(t)
	values := testutil.RuntimeSettingsValues()
	values["TAGGER_LLM_API_KEY"] = "file-key"
	runtimeFile := testutil.WriteRuntimeSettingsFile(t, values)
	svc := configservice.NewConfigServiceWithRuntimeSettingsFile(testutil.RawDB(t, db), runtimeFile)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings: %v", err)
	}

	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_LLM_API_KEY").First(&item).Error; err != nil {
		t.Fatalf("get tagger API key item: %v", err)
	}
	if item.Value != "file-key" || !item.WriteOnly || item.IsRequired {
		t.Errorf("seeded tagger API key item = %+v", item)
	}

	settings, err := svc.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load runtime settings: %v", err)
	}
	if settings.TaggerLLMAPIKey != "file-key" {
		t.Errorf("TaggerLLMAPIKey = %q, want file-key", settings.TaggerLLMAPIKey)
	}

	values["TAGGER_LLM_API_KEY"] = "replacement-key"
	replacementFile := testutil.WriteRuntimeSettingsFile(t, values)
	replacementSvc := configservice.NewConfigServiceWithRuntimeSettingsFile(testutil.RawDB(t, db), replacementFile)
	if err := replacementSvc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings second time: %v", err)
	}
	if err := db.Where("name = ?", "TAGGER_LLM_API_KEY").First(&item).Error; err != nil {
		t.Fatalf("get tagger API key item after reseed: %v", err)
	}
	if item.Value != "file-key" {
		t.Errorf("tagger API key = %q, want existing file-key", item.Value)
	}
}

func TestConfigService_SeedsMissingItemsFromLegacyValuesOnce(t *testing.T) {
	svc, db := setupConfigServiceDB(t)
	initial := map[string]string{
		"TAGGER_LLM_BASE_URL":            "https://legacy.example/v1",
		"TAGGER_LLM_MODEL":               "legacy-model",
		"TAGGER_AGENT_POOL_SIZE":         "6",
		"TAGGER_CONTEXT_TOKENS":          "24000",
		"TAGGER_TIMEOUT":                 "12s",
		"RECORD_PHOTO_MAX_SIZE_BYTES":    "2048",
		"SERVICE_DATE_REMINDER_TEMPLATE": "旧模板 {{.Room}}",
		"MESSAGE_GATEWAY_URL":            "https://legacy.example",
		"MESSAGE_GATEWAY_ACCOUNT":        "legacy-account",
		"MESSAGE_GATEWAY_ACCOUNT_TOKEN":  "legacy-token",
		"MESSAGE_GATEWAY_TIMEOUT":        "8s",
	}
	if err := svc.EnsureRuntimeSettingsWithInitialValues(initial); err != nil {
		t.Fatalf("ensure runtime settings: %v", err)
	}

	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_AGENT_POOL_SIZE").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Value != "6" {
		t.Errorf("pool size = %q, want legacy value 6", item.Value)
	}
	item = configmodel.ClinicConfigItem{}
	if err := db.Where("name = ?", "MESSAGE_GATEWAY_ACCOUNT_TOKEN").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Value != "legacy-token" {
		t.Errorf("gateway token = %q, want legacy-token", item.Value)
	}

	replacement := map[string]string{"TAGGER_AGENT_POOL_SIZE": "12"}
	if err := svc.EnsureRuntimeSettingsWithInitialValues(replacement); err != nil {
		t.Fatal(err)
	}
	item = configmodel.ClinicConfigItem{}
	if err := db.Where("name = ?", "TAGGER_AGENT_POOL_SIZE").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Value != "6" {
		t.Errorf("pool size after reseed = %q, want existing value 6", item.Value)
	}
}

func TestConfigService_RequiresCompleteExternalRuntimeSettingsFile(t *testing.T) {
	_, db := setupConfigServiceDB(t)
	rawDB := testutil.RawDB(t, db)

	tests := []struct {
		name string
		path func(t *testing.T) string
	}{
		{
			name: "missing file",
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing.json")
			},
		},
		{
			name: "missing item",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				delete(values, "STAFF_VERSION")
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "unknown item",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["UNKNOWN_SETTING"] = "value"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid value",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["RECORD_ACTION_WINDOW"] = "later"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid tagger URL",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["TAGGER_LLM_BASE_URL"] = "api.example.com/v1"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid tagger pool size",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["TAGGER_AGENT_POOL_SIZE"] = "0"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid tagger context",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["TAGGER_CONTEXT_TOKENS"] = "-1"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid tagger timeout",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["TAGGER_TIMEOUT"] = "0s"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid photo size",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["RECORD_PHOTO_MAX_SIZE_BYTES"] = "0"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid gateway account",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["MESSAGE_GATEWAY_ACCOUNT"] = "bad/account"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid gateway URL",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["MESSAGE_GATEWAY_URL"] = "message-gateway:8081"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid gateway timeout",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["MESSAGE_GATEWAY_TIMEOUT"] = "0s"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
		{
			name: "invalid reminder template",
			path: func(t *testing.T) string {
				values := testutil.RuntimeSettingsValues()
				values["SERVICE_DATE_REMINDER_TEMPLATE"] = "{{.Missing}}"
				return testutil.WriteRuntimeSettingsFile(t, values)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := configservice.NewConfigServiceWithRuntimeSettingsFile(rawDB, tt.path(t))
			if err := svc.EnsureRuntimeSettings(); err == nil {
				t.Fatal("expected external runtime settings validation error")
			}
		})
	}
}

func TestConfigService_ExternalRuntimeSettingsUseMessageDefaults(t *testing.T) {
	_, db := setupConfigServiceDB(t)
	values := testutil.RuntimeSettingsValues()
	for _, name := range []string{
		"SERVICE_DATE_REMINDER_TEMPLATE",
		"MESSAGE_GATEWAY_URL",
		"MESSAGE_GATEWAY_ACCOUNT",
		"MESSAGE_GATEWAY_ACCOUNT_TOKEN",
		"MESSAGE_GATEWAY_TIMEOUT",
	} {
		delete(values, name)
	}
	runtimeFile := testutil.WriteRuntimeSettingsFile(t, values)
	svc := configservice.NewConfigServiceWithRuntimeSettingsFile(testutil.RawDB(t, db), runtimeFile)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("ensure runtime settings: %v", err)
	}

	settings, err := svc.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load runtime settings: %v", err)
	}
	if settings.ServiceDateReminderTemplate != configservice.DefaultServiceDateReminderTemplate ||
		settings.MessageGatewayURL != "" ||
		settings.MessageGatewayAccount != "qq" ||
		settings.MessageGatewayAccountToken != "" ||
		settings.MessageGatewayTimeout != 5*time.Second {
		t.Errorf("message defaults = %+v", settings)
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
		RoleLevel:   configmodel.ConfigRoleLevelSuperAdmin,
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
	adminRole := configmodel.ConfigRoleLevelAdmin
	if _, err := svc.UpdateItem(taggerKey.ID, configservice.UpdateItemInput{RoleLevel: &adminRole}); !errors.Is(err, configservice.ErrConfigInvalidInput) {
		t.Fatalf("expected protected tagger role error, got %v", err)
	}
}
