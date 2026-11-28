package service_test

import (
	"encoding/json"
	"testing"

	"clinic-backend/internal/journal"
	journalmodel "clinic-backend/internal/journal/model"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

func setupCampusJournalService(t *testing.T) (*roomservice.CampusService, *gorm.DB) {
	t.Helper()
	db := setupRoomServiceDB(t)
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	raw := testutil.RawDB(t, db)
	svc := roomservice.NewCampusService(raw)
	svc.SetJournalService(journal.NewService(raw))
	return svc, db
}

func TestCampusNoShowBanPolicyJournalLifecycle(t *testing.T) {
	svc, db := setupCampusJournalService(t)

	if _, err := svc.CreateForActor(roomservice.CreateCampusInput{Name: "Default"}, 41); err != nil {
		t.Fatalf("create default campus: %v", err)
	}
	var count int64
	if err := db.Model(&journalmodel.Event{}).Count(&count).Error; err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Fatalf("default policy created %d events, want 0", count)
	}

	enabled := true
	duration := int64(72)
	level := roommodel.NoShowBanLevelGlobal
	campus, err := svc.CreateForActor(roomservice.CreateCampusInput{
		Name: "Configured",
		NoShowBanPolicy: &roomservice.NoShowBanPolicyPatch{
			Enabled:       &enabled,
			DurationHours: &duration,
			Level:         &level,
		},
	}, 42)
	if err != nil {
		t.Fatalf("create configured campus: %v", err)
	}

	disabled := false
	if _, err := svc.UpdateForActor(campus.ID, roomservice.UpdateCampusInput{
		NoShowBanPolicy: &roomservice.NoShowBanPolicyPatch{Enabled: &disabled},
	}, 43); err != nil {
		t.Fatalf("update policy: %v", err)
	}
	if _, err := svc.UpdateForActor(campus.ID, roomservice.UpdateCampusInput{
		Name: strPtr("Renamed"),
	}, 44); err != nil {
		t.Fatalf("rename campus: %v", err)
	}
	if err := svc.DeleteForActor(campus.ID, 45); err != nil {
		t.Fatalf("delete configured campus: %v", err)
	}

	var events []journalmodel.Event
	if err := db.Order("id ASC").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	tests := []struct {
		action  string
		actorID string
	}{
		{action: "campus.no_show_ban_policy_created", actorID: "42"},
		{action: "campus.no_show_ban_policy_updated", actorID: "43"},
		{action: "campus.no_show_ban_policy_removed", actorID: "45"},
	}
	for i, want := range tests {
		event := events[i]
		if event.ActorType != journal.ActorStaff ||
			event.ActorID != want.actorID ||
			event.Action != want.action ||
			event.EntityType != "campus" ||
			event.EntityID == "" ||
			event.OperationID == "" {
			t.Fatalf("event %d = %+v, want action %s actor %s", i, event, want.action, want.actorID)
		}
		var details struct {
			CampusName string `json:"campus_name"`
			Changes    map[string]struct {
				Before any `json:"before"`
				After  any `json:"after"`
			} `json:"changes"`
		}
		if err := json.Unmarshal(event.Details, &details); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		if details.CampusName == "" || len(details.Changes) == 0 {
			t.Fatalf("event %d details = %+v", i, details)
		}
	}

	var updatedEvent struct {
		Changes map[string]struct {
			Before any `json:"before"`
			After  any `json:"after"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(events[1].Details, &updatedEvent); err != nil {
		t.Fatalf("decode update event: %v", err)
	}
	enabledChange, ok := updatedEvent.Changes["enabled"]
	if !ok || enabledChange.Before != true || enabledChange.After != false {
		t.Fatalf("enabled change = %+v", enabledChange)
	}
}

func TestCampusNoShowBanPolicyJournalFailureRollsBack(t *testing.T) {
	svc, db := setupCampusJournalService(t)
	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "Rollback"})
	if err != nil {
		t.Fatalf("create campus: %v", err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_campus_journal
		BEFORE INSERT ON clinic_journal_event
		BEGIN
			SELECT RAISE(ABORT, 'journal unavailable');
		END
	`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	enabled := true
	if _, err := svc.UpdateForActor(campus.ID, roomservice.UpdateCampusInput{
		NoShowBanPolicy: &roomservice.NoShowBanPolicyPatch{Enabled: &enabled},
	}, 42); err == nil {
		t.Fatal("expected policy update to fail")
	}
	stored, err := svc.GetByID(campus.ID)
	if err != nil {
		t.Fatalf("reload campus: %v", err)
	}
	if stored.NoShowBanPolicy.Enabled {
		t.Fatalf("policy update was not rolled back: %+v", stored.NoShowBanPolicy)
	}
}
