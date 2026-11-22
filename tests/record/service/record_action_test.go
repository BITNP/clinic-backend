package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

// fakeActionStore is an in-memory services.RecordActionStore for tests.
type fakeActionStore struct {
	entries map[int][]recordservice.RecordActionEntry
}

type fixedOrderActionStore struct {
	*fakeActionStore
	entries []recordservice.RecordActionEntry
}

func (f *fixedOrderActionStore) Window(_ context.Context, _ int, _ time.Time) ([]recordservice.RecordActionEntry, error) {
	return append([]recordservice.RecordActionEntry(nil), f.entries...), nil
}

func TestRecordActionsOrderedWithinSameMillisecond(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	store := &fixedOrderActionStore{fakeActionStore: newFakeActionStore(), entries: []recordservice.RecordActionEntry{
		{RecordID: 1, ActionSeq: 1, CreatedAt: now.Add(100 * time.Microsecond)},
		{RecordID: 1, ActionSeq: 2, CreatedAt: now.Add(900 * time.Microsecond)},
	}}
	svc := recordservice.NewRecordActionService(store, time.Minute)
	windows, err := svc.Windows(context.Background(), 1, now.Add(time.Second))
	if err != nil || windows[1].Latest.ActionSeq != 2 || windows[1].Count != 2 {
		t.Fatalf("latest action: %+v, %v", windows[1], err)
	}
}

func newFakeActionStore() *fakeActionStore {
	return &fakeActionStore{entries: make(map[int][]recordservice.RecordActionEntry)}
}

func (f *fakeActionStore) Push(_ context.Context, actorID int, entry recordservice.RecordActionEntry, _ time.Duration) error {
	f.entries[actorID] = append(f.entries[actorID], entry)
	return nil
}

func (f *fakeActionStore) Window(_ context.Context, actorID int, cutoff time.Time) ([]recordservice.RecordActionEntry, error) {
	kept := make([]recordservice.RecordActionEntry, 0, len(f.entries[actorID]))
	for _, e := range f.entries[actorID] {
		if e.CreatedAt.After(cutoff) {
			kept = append(kept, e)
		}
	}
	f.entries[actorID] = kept

	out := append([]recordservice.RecordActionEntry(nil), kept...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (f *fakeActionStore) Remove(_ context.Context, actorID int, entry recordservice.RecordActionEntry) error {
	kept := f.entries[actorID][:0]
	for _, e := range f.entries[actorID] {
		if e.RecordID == entry.RecordID && e.ActionSeq == entry.ActionSeq && e.ActorID == entry.ActorID && e.ActionToken == entry.ActionToken {
			continue
		}
		kept = append(kept, e)
	}
	f.entries[actorID] = kept
	return nil
}

func newActionRecordService(t *testing.T, db *gorm.DB, store *fakeActionStore) *recordservice.AdminRecordService {
	svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
	svc.SetRecordActionService(recordservice.NewRecordActionService(store, 5*time.Minute))
	return svc
}

func seedActionRoom(t *testing.T, db *gorm.DB) roommodel.ClinicRoom {
	t.Helper()
	room := roommodel.ClinicRoom{Name: "A"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	return room
}

func seedActionRecord(t *testing.T, db *gorm.DB, status recordmodel.RecordStatus, roomID uint) recordmodel.ClinicRecord {
	t.Helper()
	rec := recordmodel.ClinicRecord{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Status:          status,
		AppointmentTime: time.Now().UTC().AddDate(0, 0, 1),
		QuestionDesc:    "x",
		RoomID:          roomID,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}
	return rec
}

func reloadActionRecord(t *testing.T, db *gorm.DB, id uint) recordmodel.ClinicRecord {
	t.Helper()
	var rec recordmodel.ClinicRecord
	if err := db.First(&rec, id).Error; err != nil {
		t.Fatalf("reload record: %v", err)
	}
	return rec
}

func TestRevertConfirmRestoresPending(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	v, err := svc.MarkConfirmed(context.Background(), rec.ID, 7)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if v.Status != string(recordmodel.RecordStatusConfirmed) || !v.Revertible {
		t.Fatalf("unexpected view after confirm: %+v", v)
	}

	got, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got.Status != string(recordmodel.RecordStatusPending) {
		t.Fatalf("expected pending, got %s", got.Status)
	}

	rec2 := reloadActionRecord(t, db, rec.ID)
	if rec2.ApproverID != nil {
		t.Fatalf("expected approver cleared, got %v", *rec2.ApproverID)
	}
	if rec2.ActionSeq != 0 {
		t.Fatalf("expected action_seq 0, got %d", rec2.ActionSeq)
	}
}

func TestRevertConfirmRestoresOriginalRoom(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	source := seedActionRoom(t, db)
	destination := roommodel.ClinicRoom{Name: "B", Address: "B address", Enabled: true}
	if err := db.Create(&destination).Error; err != nil {
		t.Fatal(err)
	}
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, source.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.MarkConfirmedToRoom(context.Background(), rec.ID, &destination.ID, 7); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if got := reloadActionRecord(t, db, rec.ID); got.RoomID != destination.ID {
		t.Fatalf("confirmed room = %d, want %d", got.RoomID, destination.ID)
	}
	if _, err := svc.Revert(context.Background(), 7, rec.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got := reloadActionRecord(t, db, rec.ID); got.RoomID != source.ID {
		t.Fatalf("reverted room = %d, want %d", got.RoomID, source.ID)
	}
}

func TestRevertRejectRemovesRejection(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.MarkRejected(context.Background(), rec.ID, "bad", 7); err != nil {
		t.Fatalf("reject: %v", err)
	}
	var count int64
	db.Model(&recordmodel.ClinicRecordRejection{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 1 {
		t.Fatalf("expected rejection row, got %d", count)
	}

	got, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got.Status != string(recordmodel.RecordStatusPending) || got.RejectReason != "" {
		t.Fatalf("unexpected reverted view: %+v", got)
	}
	db.Model(&recordmodel.ClinicRecordRejection{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Fatalf("expected rejection row removed, got %d", count)
	}
}

func TestRevertArriveRemovesArrival(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusConfirmed, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.MarkArrived(context.Background(), 7, rec.ID); err != nil {
		t.Fatalf("arrive: %v", err)
	}
	var count int64
	db.Model(&recordmodel.ClinicRecordArrival{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 1 {
		t.Fatalf("expected arrival row, got %d", count)
	}

	got, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got.Status != string(recordmodel.RecordStatusConfirmed) || got.ArriveTime != nil {
		t.Fatalf("unexpected reverted view: %+v", got)
	}
	db.Model(&recordmodel.ClinicRecordArrival{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Fatalf("expected arrival row removed, got %d", count)
	}
}

func TestRevertInProgressRestoresWorker(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusArrived, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.MarkInProgress(context.Background(), 7, rec.ID); err != nil {
		t.Fatalf("in-progress: %v", err)
	}
	var count int64
	db.Model(&recordmodel.ClinicRecordWorker{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 1 {
		t.Fatalf("expected worker row, got %d", count)
	}

	got, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got.Status != string(recordmodel.RecordStatusArrived) || got.WorkerID != nil {
		t.Fatalf("unexpected reverted view: %+v", got)
	}
	db.Model(&recordmodel.ClinicRecordWorker{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Fatalf("expected worker row removed, got %d", count)
	}
}

func TestRevertCompleteClearsFinishTime(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusInProgress, room.ID)
	if err := db.Create(&recordmodel.ClinicRecordWorker{RecordID: rec.ID, WorkerID: 7}).Error; err != nil {
		t.Fatalf("seed worker: %v", err)
	}
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.MarkCompleted(context.Background(), 7, rec.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	worker := recordmodel.ClinicRecordWorker{}
	db.Where("record_id = ?", rec.ID).First(&worker)
	if worker.FinishTime.IsZero() {
		t.Fatalf("expected finish_time set")
	}

	got, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got.Status != string(recordmodel.RecordStatusInProgress) || got.FinishTime != nil {
		t.Fatalf("unexpected reverted view: %+v", got)
	}
}

func TestRevertReferRemovesReferral(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.MarkReferred(context.Background(), rec.ID, "return", 7); err != nil {
		t.Fatalf("refer: %v", err)
	}
	got, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got.Status != string(recordmodel.RecordStatusPending) || got.ReferralReason != "" {
		t.Fatalf("unexpected reverted view: %+v", got)
	}
	var count int64
	db.Model(&recordmodel.ClinicRecordReferral{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Fatalf("expected referral row removed, got %d", count)
	}
}

func TestRevertUpdateRestoresWorkerDesc(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	desc := "fixed it"
	if _, err := svc.Update(context.Background(), 7, rec.ID, recordservice.UpdateAdminRecordInput{WorkerDesc: &desc}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got.WorkerDesc != "" {
		t.Fatalf("expected worker_desc cleared, got %q", got.WorkerDesc)
	}
	var count int64
	db.Model(&recordmodel.ClinicRecordWorker{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Fatalf("expected worker row removed, got %d", count)
	}
}

func TestRevertStrictLIFO(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.MarkConfirmed(context.Background(), rec.ID, 7); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := svc.MarkArrived(context.Background(), 7, rec.ID); err != nil {
		t.Fatalf("arrive: %v", err)
	}

	got, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("first revert: %v", err)
	}
	if got.Status != string(recordmodel.RecordStatusConfirmed) || got.RevertibleCount != 1 {
		t.Fatalf("unexpected after first revert: %+v", got)
	}
	if !got.Revertible {
		t.Fatalf("confirm should still be revertible after arrive was undone: %+v", got)
	}

	got, err = svc.Revert(context.Background(), 7, rec.ID)
	if err != nil {
		t.Fatalf("second revert: %v", err)
	}
	if got.Status != string(recordmodel.RecordStatusPending) {
		t.Fatalf("expected pending after second revert, got %s", got.Status)
	}
}

func TestRevertSupersededByAnotherActor(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.MarkConfirmed(context.Background(), rec.ID, 1); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := svc.MarkArrived(context.Background(), 2, rec.ID); err != nil {
		t.Fatalf("arrive: %v", err)
	}

	if _, err := svc.Revert(context.Background(), 1, rec.ID); !errors.Is(err, recordservice.ErrRevertSuperseded) {
		t.Fatalf("expected superseded, got %v", err)
	}
	if _, err := svc.Revert(context.Background(), 2, rec.ID); err != nil {
		t.Fatalf("second actor revert: %v", err)
	}
}

func TestRevertRejectsStaleActionWithReusedSequence(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	store := newFakeActionStore()
	svc := newActionRecordService(t, db, store)
	ctx := context.Background()
	if _, err := svc.MarkConfirmed(ctx, rec.ID, 1); err != nil {
		t.Fatal(err)
	}
	stale := store.entries[1][0]
	if stale.ActionToken == "" {
		t.Fatal("action token is empty")
	}
	if _, err := svc.Revert(ctx, 1, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkConfirmed(ctx, rec.ID, 2); err != nil {
		t.Fatal(err)
	}
	current := reloadActionRecord(t, db, rec.ID)
	if current.ActionSeq != stale.ActionSeq || current.ActionToken == stale.ActionToken {
		t.Fatalf("expected reused sequence and distinct token: record=%+v stale=%+v", current, stale)
	}
	store.entries[1] = append(store.entries[1], stale)
	view, err := svc.GetByID(ctx, 1, rec.ID)
	if err != nil || view.Revertible {
		t.Fatalf("stale action marked revertible: %+v, %v", view, err)
	}
	if _, err := svc.Revert(ctx, 1, rec.ID); !errors.Is(err, recordservice.ErrRevertSuperseded) {
		t.Fatalf("stale revert: %v", err)
	}
	if got := reloadActionRecord(t, db, rec.ID); got.Status != recordmodel.RecordStatusConfirmed || got.ActionToken != current.ActionToken {
		t.Fatalf("newer action was overwritten: %+v", got)
	}
}

func TestRevertLegacyActionWithoutToken(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusConfirmed, room.ID)
	if err := db.Model(&rec).Update("action_seq", 1).Error; err != nil {
		t.Fatal(err)
	}
	store := newFakeActionStore()
	store.entries[1] = []recordservice.RecordActionEntry{{RecordID: rec.ID, ActorID: 1, ActionSeq: 1, CreatedAt: time.Now().UTC(), Prev: recordservice.RecordSnapshot{Status: recordmodel.RecordStatusPending}}}
	svc := newActionRecordService(t, db, store)
	view, err := svc.Revert(context.Background(), 1, rec.ID)
	if err != nil || view.Status != string(recordmodel.RecordStatusPending) {
		t.Fatalf("revert legacy action: %+v, %v", view, err)
	}
}

func TestLegacyActionJSONRemainsRemovable(t *testing.T) {
	// Redis removes the exact encoded member, so decoding and re-encoding an
	// old entry must not add fields or otherwise change its representation.
	legacy := `{"record_id":3,"actor_id":1,"action":"confirm","prev_seq":0,"action_seq":1,"created_at":"2026-01-01T00:00:00Z","prev":{"status":"pending","approver_id":null}}`
	var entry recordservice.RecordActionEntry
	if err := json.Unmarshal([]byte(legacy), &entry); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil || string(encoded) != legacy {
		t.Fatalf("legacy Redis member changed: %s, %v", encoded, err)
	}
}

func TestActionTokenMigrationPreservesExistingRecords(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	if err := db.Exec("ALTER TABLE clinic_record DROP COLUMN action_token").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&recordmodel.ClinicRecord{}); err != nil {
		t.Fatal(err)
	}
	got := reloadActionRecord(t, db, rec.ID)
	if got.ActionToken != "" || got.Status != rec.Status || got.User != rec.User {
		t.Fatalf("record after token migration: %+v", got)
	}
}

func TestRevertUnavailableWhenNoAction(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	if _, err := svc.Revert(context.Background(), 7, rec.ID); !errors.Is(err, recordservice.ErrRevertUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestRevertExpiredWindowIsPruned(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	store := newFakeActionStore()
	svc := newActionRecordService(t, db, store)

	// A journaled action whose window has already lapsed.
	if err := store.Push(context.Background(), 7, recordservice.RecordActionEntry{
		RecordID:  rec.ID,
		ActorID:   7,
		Action:    recordservice.RecordActionConfirm,
		PrevSeq:   0,
		ActionSeq: 1,
		CreatedAt: time.Now().UTC().Add(-10 * time.Minute),
		Prev:      recordservice.RecordSnapshot{Status: recordmodel.RecordStatusPending},
	}, 5*time.Minute); err != nil {
		t.Fatalf("seed action: %v", err)
	}

	if _, err := svc.Revert(context.Background(), 7, rec.ID); !errors.Is(err, recordservice.ErrRevertUnavailable) {
		t.Fatalf("expected unavailable for expired action, got %v", err)
	}
}

func TestRevertInvalidatedBySystemCleanup(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())

	// Make the appointment past so the nightly cleanup closes the record.
	if err := db.Model(&recordmodel.ClinicRecord{}).
		Where("id = ?", rec.ID).
		Update("appointment_time", time.Now().UTC().AddDate(0, 0, -2)).Error; err != nil {
		t.Fatalf("age record: %v", err)
	}

	if _, err := svc.MarkConfirmed(context.Background(), rec.ID, 7); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, _, err := svc.CloseExpiredRecords(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, err := svc.Revert(context.Background(), 7, rec.ID); !errors.Is(err, recordservice.ErrRevertSuperseded) {
		t.Fatalf("expected superseded after system cleanup, got %v", err)
	}
}

func TestRecordActionEntryJSONRoundTrip(t *testing.T) {
	arrive := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	approver := uint(3)
	reason := "bad"
	entry := recordservice.RecordActionEntry{
		RecordID:  5,
		ActorID:   7,
		Action:    recordservice.RecordActionReject,
		PrevSeq:   2,
		ActionSeq: 3,
		CreatedAt: time.Date(2026, 9, 24, 12, 0, 0, 123456789, time.UTC),
		Prev: recordservice.RecordSnapshot{
			Status:         recordmodel.RecordStatusConfirmed,
			ApproverID:     &approver,
			ArriveTime:     &arrive,
			Worker:         &recordservice.WorkerSnapshot{WorkerID: 9, WorkerDesc: "d"},
			RejectReason:   &reason,
			ReferralReason: &reason,
		},
	}
	first, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed recordservice.RecordActionEntry
	if err := json.Unmarshal(first, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("json not stable:\n%s\n%s", first, second)
	}
}

func TestRevertibleFieldsPerViewer(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(t, db, newFakeActionStore())
	ctx := context.Background()

	if _, err := svc.MarkConfirmed(ctx, rec.ID, 7); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	own, err := svc.GetByID(ctx, 7, rec.ID)
	if err != nil {
		t.Fatalf("get own: %v", err)
	}
	if !own.Revertible || own.RevertibleCount != 1 || own.RevertibleUntil == nil || own.LastActionAt == nil {
		t.Fatalf("expected own view revertible: %+v", own)
	}

	other, err := svc.GetByID(ctx, 99, rec.ID)
	if err != nil {
		t.Fatalf("get other: %v", err)
	}
	if other.Revertible || other.RevertibleCount != 0 {
		t.Fatalf("expected other view not revertible: %+v", other)
	}
}
