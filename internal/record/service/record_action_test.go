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

	"gorm.io/gorm"
)

// fakeActionStore is an in-memory services.RecordActionStore for tests.
type fakeActionStore struct {
	entries map[int][]recordservice.RecordActionEntry
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
		if e.RecordID == entry.RecordID && e.ActionSeq == entry.ActionSeq && e.ActorID == entry.ActorID {
			continue
		}
		kept = append(kept, e)
	}
	f.entries[actorID] = kept
	return nil
}

func newActionRecordService(db *gorm.DB, store *fakeActionStore) *recordservice.AdminRecordService {
	svc := recordservice.NewAdminRecordService(db)
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
	svc := newActionRecordService(db, newFakeActionStore())

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

func TestRevertRejectRemovesRejection(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(db, newFakeActionStore())

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
	svc := newActionRecordService(db, newFakeActionStore())

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
	svc := newActionRecordService(db, newFakeActionStore())

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
	svc := newActionRecordService(db, newFakeActionStore())

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
	svc := newActionRecordService(db, newFakeActionStore())

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
	svc := newActionRecordService(db, newFakeActionStore())

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
	svc := newActionRecordService(db, newFakeActionStore())

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
	svc := newActionRecordService(db, newFakeActionStore())

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

func TestRevertUnavailableWhenNoAction(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	svc := newActionRecordService(db, newFakeActionStore())

	if _, err := svc.Revert(context.Background(), 7, rec.ID); !errors.Is(err, recordservice.ErrRevertUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestRevertExpiredWindowIsPruned(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	rec := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	store := newFakeActionStore()
	svc := newActionRecordService(db, store)

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
	svc := newActionRecordService(db, newFakeActionStore())

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
	svc := newActionRecordService(db, newFakeActionStore())
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
