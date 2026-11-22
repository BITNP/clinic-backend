package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	configmodel "clinic-backend/internal/config/model"
	configservice "clinic-backend/internal/config/service"
	"clinic-backend/internal/journal"
	journalmodel "clinic-backend/internal/journal/model"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

func setupConfigJournalService(t *testing.T) (*configservice.ConfigService, *gorm.DB) {
	t.Helper()
	svc, db := setupConfigServiceDB(t)
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	svc.SetJournalService(journal.NewService(testutil.RawDB(t, db)))
	return svc, db
}

func createJournalConfigItem(
	t *testing.T,
	svc *configservice.ConfigService,
	name, value string,
	writeOnly bool,
) configmodel.ClinicConfigItem {
	t.Helper()
	group, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "journal_" + strings.ToLower(name),
		DisplayName: "Journal",
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	roleLevel := configmodel.ConfigRoleLevelAdmin
	if name == "MESSAGE_GATEWAY_ACCOUNT_TOKEN" {
		roleLevel = configmodel.ConfigRoleLevelSuperAdmin
	}
	item, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        name,
		Value:       value,
		ValueType:   configmodel.ConfigValueTypeString,
		DisplayName: name,
		RoleLevel:   roleLevel,
		WriteOnly:   writeOnly,
		GroupID:     group.ID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	return item
}

func TestConfigItemUpdateWritesJournalEvent(t *testing.T) {
	svc, db := setupConfigJournalService(t)
	item := createJournalConfigItem(t, svc, "REVIEW_LIMIT", "5", false)
	value := "8"

	if _, err := svc.UpdateItemForActor(
		configmodel.ConfigRoleLevelAdmin,
		42,
		item.ID,
		configservice.UpdateItemInput{Value: &value},
	); err != nil {
		t.Fatalf("update item: %v", err)
	}

	var event journalmodel.Event
	if err := db.First(&event).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if event.ActorID != "42" || event.Action != "config_item.updated" {
		t.Fatalf("unexpected event: %+v", event)
	}
	var details struct {
		Changes map[string]struct {
			Before any `json:"before"`
			After  any `json:"after"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(event.Details, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details.Changes["value"].Before != "5" || details.Changes["value"].After != "8" {
		t.Fatalf("value change = %+v", details.Changes["value"])
	}
}

func TestConfigItemJournalRedactsSecrets(t *testing.T) {
	svc, db := setupConfigJournalService(t)
	item := createJournalConfigItem(t, svc, "MESSAGE_GATEWAY_ACCOUNT_TOKEN", "old-secret", true)
	value := "new-secret"

	if _, err := svc.UpdateItemForActor(
		configmodel.ConfigRoleLevelSuperAdmin,
		7,
		item.ID,
		configservice.UpdateItemInput{Value: &value},
	); err != nil {
		t.Fatalf("update item: %v", err)
	}

	var event journalmodel.Event
	if err := db.First(&event).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	payload := string(event.Details)
	if strings.Contains(payload, "old-secret") || strings.Contains(payload, "new-secret") {
		t.Fatalf("journal contains secret value: %s", payload)
	}
	if !strings.Contains(payload, `"redacted":true`) {
		t.Fatalf("journal does not mark value redacted: %s", payload)
	}
}

func TestConfigItemJournalFailureRollsBackUpdate(t *testing.T) {
	svc, db := setupConfigJournalService(t)
	item := createJournalConfigItem(t, svc, "REVIEW_LIMIT", "5", false)
	if err := db.Exec(`
		CREATE TRIGGER reject_config_journal
		BEFORE INSERT ON clinic_journal_event
		BEGIN
			SELECT RAISE(ABORT, 'journal unavailable');
		END
	`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	value := "8"
	if _, err := svc.UpdateItemForActor(
		configmodel.ConfigRoleLevelAdmin,
		42,
		item.ID,
		configservice.UpdateItemInput{Value: &value},
	); err == nil {
		t.Fatal("expected update to fail")
	}
	stored, err := svc.GetItem(item.ID)
	if err != nil {
		t.Fatalf("reload item: %v", err)
	}
	if stored.Value != "5" {
		t.Fatalf("stored value = %q, want 5", stored.Value)
	}
}
