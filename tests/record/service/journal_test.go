package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"clinic-backend/internal/journal"
	journalmodel "clinic-backend/internal/journal/model"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	"clinic-backend/internal/testutil"
)

func TestRecordTransitionWritesJournalEvent(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	rawDB := testutil.RawDB(t, db)
	svc := recordservice.NewAdminRecordService(rawDB)
	svc.SetJournalService(journal.NewService(rawDB))

	room := roommodel.ClinicRoom{Name: "Journal Room", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	record := recordmodel.ClinicRecord{
		User:         "customer",
		Realname:     "Customer",
		PhoneNum:     "123",
		Status:       recordmodel.RecordStatusPending,
		QuestionDesc: "Problem",
		RoomID:       room.ID,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	if _, err := svc.MarkConfirmed(context.Background(), record.ID, 7); err != nil {
		t.Fatalf("confirm record: %v", err)
	}
	var event journalmodel.Event
	if err := db.First(&event).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if event.ActorType != journal.ActorStaff || event.ActorID != "7" ||
		event.Action != "record.confirmed" || event.EntityType != "record" {
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
	if details.Changes["status"].Before != "pending" || details.Changes["status"].After != "confirmed" {
		t.Fatalf("status change = %+v", details.Changes["status"])
	}
	if details.Changes["approver_id"].After != float64(7) {
		t.Fatalf("approver change = %+v", details.Changes["approver_id"])
	}
}

func TestRecordTransitionRollsBackWhenJournalFails(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	rawDB := testutil.RawDB(t, db)
	svc := recordservice.NewAdminRecordService(rawDB)
	svc.SetJournalService(journal.NewService(rawDB))

	room := roommodel.ClinicRoom{Name: "Journal Room", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	record := recordmodel.ClinicRecord{
		User:         "customer",
		Realname:     "Customer",
		PhoneNum:     "123",
		Status:       recordmodel.RecordStatusPending,
		QuestionDesc: "Problem",
		RoomID:       room.ID,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_record_journal
		BEFORE INSERT ON clinic_journal_event
		BEGIN
			SELECT RAISE(ABORT, 'journal unavailable');
		END
	`).Error; err != nil {
		t.Fatalf("create journal rejection trigger: %v", err)
	}

	if _, err := svc.MarkConfirmed(context.Background(), record.ID, 7); err == nil {
		t.Fatal("expected confirmation to fail")
	}
	var stored recordmodel.ClinicRecord
	if err := db.First(&stored, record.ID).Error; err != nil {
		t.Fatalf("reload record: %v", err)
	}
	if stored.Status != recordmodel.RecordStatusPending {
		t.Fatalf("record status = %q, want pending", stored.Status)
	}
}

func TestRecordUpdateAndRevertWriteLinkedJournalEvents(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	rawDB := testutil.RawDB(t, db)
	svc := recordservice.NewAdminRecordService(rawDB)
	svc.SetJournalService(journal.NewService(rawDB))
	svc.SetRecordActionService(recordservice.NewRecordActionService(newFakeActionStore(), 5*time.Minute))

	room := roommodel.ClinicRoom{Name: "Journal Room", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	record := recordmodel.ClinicRecord{
		User:         "customer",
		Realname:     "Customer",
		PhoneNum:     "123",
		Status:       recordmodel.RecordStatusConfirmed,
		QuestionDesc: "Problem",
		RoomID:       room.ID,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}
	note := "replaced cable"
	if _, err := svc.Update(context.Background(), 42, record.ID, recordservice.UpdateAdminRecordInput{
		WorkerDesc: &note,
	}); err != nil {
		t.Fatalf("update notes: %v", err)
	}
	if _, err := svc.Revert(context.Background(), 42, record.ID); err != nil {
		t.Fatalf("revert notes: %v", err)
	}

	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 2 || events[0].Action != "record.notes_updated" ||
		events[1].Action != "record.reverted" {
		t.Fatalf("events = %+v", events)
	}
	var details struct {
		RevertedEventID uint64 `json:"reverted_event_id"`
		RevertedAction  string `json:"reverted_action"`
	}
	if err := json.Unmarshal(events[1].Details, &details); err != nil {
		t.Fatalf("decode revert details: %v", err)
	}
	if details.RevertedEventID != events[0].ID || details.RevertedAction != "record.notes_updated" {
		t.Fatalf("revert details = %+v, first event = %d", details, events[0].ID)
	}
}
