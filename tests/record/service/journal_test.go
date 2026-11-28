package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/journal"
	journalmodel "clinic-backend/internal/journal/model"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
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

func TestRecordTransitionJournalsNotificationSent(t *testing.T) {
	notifier := &fakeTicketStatusNotifier{}
	svc, db, record := setupTicketNotificationJournal(t, notifier)

	if _, err := svc.MarkConfirmed(context.Background(), record.ID, 7); err != nil {
		t.Fatalf("confirm record: %v", err)
	}

	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2: %+v", len(events), events)
	}
	recordEvent, sentEvent := events[0], events[1]
	if recordEvent.Action != "record.confirmed" || sentEvent.Action != "notification.sent" {
		t.Fatalf("actions = %q, %q", recordEvent.Action, sentEvent.Action)
	}
	if sentEvent.EntityType != "record" || sentEvent.EntityID != testutil.Itoa(record.ID) {
		t.Fatalf("sent entity = %s/%s", sentEvent.EntityType, sentEvent.EntityID)
	}
	if sentEvent.CausedByEventID == nil || *sentEvent.CausedByEventID != recordEvent.ID {
		t.Fatalf("sent cause = %v, want record event %d", sentEvent.CausedByEventID, recordEvent.ID)
	}

	var details map[string]any
	if err := json.Unmarshal(sentEvent.Details, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details["kind"] != "record_status" ||
		details["channel"] != "dingtalk" ||
		details["template_key"] != "DINGTALK_TEMPLATE_CONFIRMED" ||
		details["record_status"] != "confirmed" ||
		details["message_center_msg_id"] != "ticket-message-1" {
		t.Fatalf("sent details = %+v", details)
	}
	if strings.Contains(string(sentEvent.Details), "secret-user") {
		t.Fatalf("journal contains recipient id: %s", sentEvent.Details)
	}
}

func TestRecordTransitionJournalsNotificationFailure(t *testing.T) {
	notifier := &fakeTicketStatusNotifier{err: errors.New("message center unavailable: secret@example.com")}
	svc, db, record := setupTicketNotificationJournal(t, notifier)

	if _, err := svc.MarkConfirmed(context.Background(), record.ID, 7); err != nil {
		t.Fatalf("confirm record: %v", err)
	}

	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2: %+v", len(events), events)
	}
	failed := events[1]
	if failed.Action != "notification.failed" {
		t.Fatalf("action = %q, want notification.failed", failed.Action)
	}
	if failed.CausedByEventID == nil || *failed.CausedByEventID != events[0].ID {
		t.Fatalf("failed cause = %v, want %d", failed.CausedByEventID, events[0].ID)
	}
	if strings.Contains(string(failed.Details), "secret@example.com") {
		t.Fatalf("journal leaked raw error: %s", failed.Details)
	}
	var details map[string]any
	if err := json.Unmarshal(failed.Details, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details["reason"] != "dingtalk_delivery_failed" {
		t.Fatalf("failure details = %+v", details)
	}
}

func TestRecordTransitionJournalsMissingNotificationContext(t *testing.T) {
	notifier := &fakeTicketStatusNotifier{}
	svc, db, record := setupTicketNotificationJournal(t, notifier)
	if err := db.Exec("DELETE FROM clinic_service_date").Error; err != nil {
		t.Fatalf("delete service date: %v", err)
	}

	if _, err := svc.MarkConfirmed(context.Background(), record.ID, 7); err != nil {
		t.Fatalf("confirm record: %v", err)
	}

	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 2 || events[1].Action != "notification.failed" {
		t.Fatalf("events = %+v", events)
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("notifications = %d, want 0", len(notifier.notifications))
	}
	var details map[string]any
	if err := json.Unmarshal(events[1].Details, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details["reason"] != "notification_context_unavailable" {
		t.Fatalf("details = %+v", details)
	}
}

func TestNotificationJournalFailureDoesNotFailSentTransition(t *testing.T) {
	notifier := &fakeTicketStatusNotifier{}
	svc, db, record := setupTicketNotificationJournal(t, notifier)
	if err := db.Exec(`
		CREATE TRIGGER reject_notification_journal
		BEFORE INSERT ON clinic_journal_event
		WHEN NEW.action LIKE 'notification.%'
		BEGIN
			SELECT RAISE(ABORT, 'notification journal unavailable');
		END
	`).Error; err != nil {
		t.Fatalf("create notification journal rejection trigger: %v", err)
	}

	view, err := svc.MarkConfirmed(context.Background(), record.ID, 7)
	if err != nil {
		t.Fatalf("confirm record: %v", err)
	}
	if view.Status != string(recordmodel.RecordStatusConfirmed) {
		t.Fatalf("status = %q, want confirmed", view.Status)
	}
	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notifier.notifications))
	}
	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 || events[0].Action != "record.confirmed" {
		t.Fatalf("events = %+v, want only record.confirmed", events)
	}
}

func TestRecordTransitionSkipsNotificationJournalWhenNotifierSkips(t *testing.T) {
	notifier := &controlledTicketStatusNotifier{enabled: true, skipped: true}
	svc, db, record := setupTicketNotificationJournal(t, notifier)

	if _, err := svc.MarkConfirmed(context.Background(), record.ID, 7); err != nil {
		t.Fatalf("confirm record: %v", err)
	}

	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 || events[0].Action != "record.confirmed" {
		t.Fatalf("events = %+v, want only record.confirmed", events)
	}
	if notifier.calls != 1 {
		t.Fatalf("notification calls = %d, want 1 skipped call", notifier.calls)
	}
}

func TestRecordTransitionSkipsDisabledNotificationBeforeContextLookup(t *testing.T) {
	notifier := &controlledTicketStatusNotifier{enabled: false}
	svc, db, record := setupTicketNotificationJournal(t, notifier)
	if err := db.Exec("DELETE FROM clinic_service_date").Error; err != nil {
		t.Fatalf("delete service date: %v", err)
	}

	if _, err := svc.MarkConfirmed(context.Background(), record.ID, 7); err != nil {
		t.Fatalf("confirm record: %v", err)
	}

	var events []journalmodel.Event
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 || events[0].Action != "record.confirmed" {
		t.Fatalf("events = %+v, want only record.confirmed", events)
	}
	if notifier.calls != 0 {
		t.Fatalf("notification calls = %d, want 0", notifier.calls)
	}
}

func TestAutomaticNoShowJournalsNotificationSent(t *testing.T) {
	notifier := &fakeTicketStatusNotifier{}
	svc, db, record := setupTicketNotificationJournal(t, notifier)
	if err := db.Model(record).Update("status", recordmodel.RecordStatusConfirmed).Error; err != nil {
		t.Fatalf("confirm seed record: %v", err)
	}

	noShow, completed, err := svc.CloseExpiredRecords(
		context.Background(),
		time.Now().UTC().AddDate(0, 0, 2),
	)
	if err != nil {
		t.Fatalf("close expired records: %v", err)
	}
	if noShow != 1 || completed != 0 {
		t.Fatalf("counts = no_show %d, completed %d", noShow, completed)
	}

	var event journalmodel.Event
	if err := db.First(&event).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if event.Action != "notification.sent" || event.CausedByEventID != nil {
		t.Fatalf("event = %+v", event)
	}
	var details map[string]any
	if err := json.Unmarshal(event.Details, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details["template_key"] != "DINGTALK_TEMPLATE_NO_SHOW_NO_BAN" ||
		details["record_status"] != "no_show" ||
		details["no_show_ban_enabled"] != false {
		t.Fatalf("details = %+v", details)
	}
}

func setupTicketNotificationJournal(
	t *testing.T,
	notifier recordservice.TicketStatusNotifier,
) (*recordservice.AdminRecordService, *gorm.DB, *recordmodel.ClinicRecord) {
	t.Helper()
	db := setupAdminRecordTestDB(t)
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	rawDB := testutil.RawDB(t, db)
	svc := recordservice.NewAdminRecordService(rawDB)
	svc.SetJournalService(journal.NewService(rawDB))
	svc.SetTicketStatusNotifier(notifier)

	room := seedActionRoom(t, db)
	record := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	record.User = "secret-user"
	if err := db.Model(&record).Update("user", record.User).Error; err != nil {
		t.Fatalf("update record user: %v", err)
	}
	seedTicketNotificationServiceDate(t, db, room.ID, &record)
	return svc, db, &record
}

type controlledTicketStatusNotifier struct {
	enabled bool
	skipped bool
	calls   int
}

func (f *controlledTicketStatusNotifier) NotificationsEnabled() bool {
	return f.enabled
}

func (f *controlledTicketStatusNotifier) Notify(
	_ context.Context,
	_ recordservice.TicketStatusNotification,
) (recordservice.TicketNotificationDelivery, error) {
	f.calls++
	if f.skipped {
		return recordservice.TicketNotificationDelivery{Skipped: true}, nil
	}
	return recordservice.TicketNotificationDelivery{MessageID: "controlled-message-1"}, nil
}

func (f *controlledTicketStatusNotifier) NotifyReminder(
	_ context.Context,
	_ recordservice.TicketStatusNotification,
) (recordservice.TicketNotificationDelivery, error) {
	return recordservice.TicketNotificationDelivery{MessageID: "controlled-reminder-1"}, nil
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
