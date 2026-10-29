package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	staffmodel "clinic-backend/internal/staff/model"
	taggingservice "clinic-backend/internal/tagging/service"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupAdminRecordTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&roommodel.ClinicRoom{},
		&recordmodel.ClinicRecord{},
		&recordmodel.ClinicRecordDevice{},
		&recordmodel.ClinicRecordWorker{},
		&recordmodel.ClinicRecordPhoto{},
		&recordmodel.ClinicRecordArrival{},
		&recordmodel.ClinicRecordRejection{},
		&recordmodel.ClinicRecordReferral{},
		&staffmodel.ClinicStaff{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func yesterdayCutoff() time.Time {
	t := time.Now().UTC().AddDate(0, 0, -1)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func TestCloseExpiredRecords_NoShow(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)
	cutoff := yesterdayCutoff()
	pastDate := cutoff.AddDate(0, 0, -1)

	room := roommodel.ClinicRoom{Name: "Test Room"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	for _, st := range []recordmodel.RecordStatus{
		recordmodel.RecordStatusPending,
		recordmodel.RecordStatusConfirmed,
		recordmodel.RecordStatusArrived,
	} {
		if err := db.Create(&recordmodel.ClinicRecord{
			User: "u", Realname: "r", PhoneNum: "p",
			Status: st, AppointmentTime: pastDate,
			QuestionDesc: "x", RoomID: room.ID,
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", st, err)
		}
	}

	noShow, completed, err := svc.CloseExpiredRecords(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("CloseExpiredRecords: %v", err)
	}
	if noShow != 3 {
		t.Errorf("expected 3 no_shows, got %d", noShow)
	}
	if completed != 0 {
		t.Errorf("expected 0 completed, got %d", completed)
	}

	var count int64
	db.Model(&recordmodel.ClinicRecord{}).Where("status = ?", recordmodel.RecordStatusNoShow).Count(&count)
	if count != 3 {
		t.Errorf("expected 3 no_show records, got %d", count)
	}
}

func TestCloseExpiredRecords_Completed(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)
	cutoff := yesterdayCutoff()
	pastDate := cutoff.AddDate(0, 0, -1)

	room := roommodel.ClinicRoom{Name: "Test Room"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	rec := recordmodel.ClinicRecord{
		User: "u", Realname: "r", PhoneNum: "p",
		Status: recordmodel.RecordStatusInProgress, AppointmentTime: pastDate,
		QuestionDesc: "x", RoomID: room.ID,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Create(&recordmodel.ClinicRecordWorker{
		RecordID:   rec.ID,
		WorkerID:   1,
		WorkerDesc: "working",
	}).Error; err != nil {
		t.Fatalf("seed worker: %v", err)
	}

	noShow, completed, err := svc.CloseExpiredRecords(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("CloseExpiredRecords: %v", err)
	}
	if noShow != 0 {
		t.Errorf("expected 0 no_shows, got %d", noShow)
	}
	if completed != 1 {
		t.Errorf("expected 1 completed, got %d", completed)
	}

	var updated recordmodel.ClinicRecord
	if err := db.First(&updated, rec.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if updated.Status != recordmodel.RecordStatusCompleted {
		t.Errorf("expected completed, got %s", updated.Status)
	}

	var worker recordmodel.ClinicRecordWorker
	if err := db.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
		t.Fatalf("reload worker: %v", err)
	}
	if worker.FinishTime.IsZero() {
		t.Errorf("expected finish_time to be set, got zero")
	}
}

func TestCloseExpiredRecords_IgnoresTerminal(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)
	cutoff := yesterdayCutoff()
	pastDate := cutoff.AddDate(0, 0, -1)

	room := roommodel.ClinicRoom{Name: "Test Room"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	for _, st := range []recordmodel.RecordStatus{
		recordmodel.RecordStatusCompleted,
		recordmodel.RecordStatusRejected,
		recordmodel.RecordStatusReferred,
		recordmodel.RecordStatusNoShow,
	} {
		if err := db.Create(&recordmodel.ClinicRecord{
			User: "u", Realname: "r", PhoneNum: "p",
			Status: st, AppointmentTime: pastDate,
			QuestionDesc: "x", RoomID: room.ID,
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", st, err)
		}
	}

	noShow, completed, err := svc.CloseExpiredRecords(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("CloseExpiredRecords: %v", err)
	}
	if noShow != 0 {
		t.Errorf("expected 0 no_shows, got %d", noShow)
	}
	if completed != 0 {
		t.Errorf("expected 0 completed, got %d", completed)
	}
}

func TestCloseExpiredRecords_IgnoresFutureTickets(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)
	cutoff := yesterdayCutoff()
	futureDate := cutoff.AddDate(0, 0, 3)

	room := roommodel.ClinicRoom{Name: "Test Room"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	for _, st := range []recordmodel.RecordStatus{
		recordmodel.RecordStatusConfirmed,
		recordmodel.RecordStatusInProgress,
	} {
		if err := db.Create(&recordmodel.ClinicRecord{
			User: "u", Realname: "r", PhoneNum: "p",
			Status: st, AppointmentTime: futureDate,
			QuestionDesc: "x", RoomID: room.ID,
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", st, err)
		}
	}

	noShow, completed, err := svc.CloseExpiredRecords(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("CloseExpiredRecords: %v", err)
	}
	if noShow != 0 {
		t.Errorf("expected 0 no_shows, got %d", noShow)
	}
	if completed != 0 {
		t.Errorf("expected 0 completed, got %d", completed)
	}
}

// countingGormLogger counts how many SQL statements GORM traces while
// delegating the actual logging to the default logger.
type countingGormLogger struct {
	logger.Interface
	n int
}

func (l *countingGormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	l.n++
	l.Interface.Trace(ctx, begin, fc, err)
}

func TestAdminRecordServiceListNewestIDsFirst(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)
	room := roommodel.ClinicRoom{Name: "Room", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	appointment := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	// Twenty older tickets have later appointments. Sorting by appointment
	// would make page one stop at #605 even though #619 already exists.
	for id := uint(586); id <= 619; id++ {
		date := appointment
		if id <= 605 {
			date = date.AddDate(0, 0, 7)
		}
		if err := db.Create(&recordmodel.ClinicRecord{
			ID: id, User: "u", Realname: "r", PhoneNum: "p",
			Status: recordmodel.RecordStatusPending, AppointmentTime: date,
			QuestionDesc: "broken", RoomID: room.ID,
		}).Error; err != nil {
			t.Fatalf("seed record %d: %v", id, err)
		}
	}

	for _, tc := range []struct {
		page  int
		count int
		first uint
	}{
		{page: 1, count: 20, first: 619},
		{page: 2, count: 14, first: 599},
	} {
		views, total, err := svc.List(context.Background(), 0, recordservice.ListAdminRecordFilter{Page: tc.page})
		if err != nil {
			t.Fatalf("list page %d: %v", tc.page, err)
		}
		if total != 34 || len(views) != tc.count {
			t.Fatalf("page %d: total=%d, count=%d; want total=34, count=%d", tc.page, total, len(views), tc.count)
		}
		for i, view := range views {
			if want := tc.first - uint(i); view.ID != want {
				t.Fatalf("page %d item %d: ID=%d, want %d", tc.page, i, view.ID, want)
			}
		}
	}

	// A subsequent refresh must include a newly created ticket even when
	// its appointment is earlier than every previously loaded ticket.
	if err := db.Create(&recordmodel.ClinicRecord{
		ID: 620, User: "u", Realname: "r", PhoneNum: "p",
		Status: recordmodel.RecordStatusPending, AppointmentTime: appointment.AddDate(0, 0, -1),
		QuestionDesc: "broken", RoomID: room.ID,
	}).Error; err != nil {
		t.Fatalf("seed newest record: %v", err)
	}
	views, total, err := svc.List(context.Background(), 0, recordservice.ListAdminRecordFilter{Page: 1})
	if err != nil {
		t.Fatalf("refresh page one: %v", err)
	}
	if total != 35 || len(views) != 20 {
		t.Fatalf("refresh: total=%d, count=%d; want total=35, count=20", total, len(views))
	}
	if views[0].ID != 620 {
		t.Errorf("refresh first ID=%d, want 620", views[0].ID)
	}
}

func TestAdminRecordServiceListBatchedViews(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)

	roomA := roommodel.ClinicRoom{Name: "Room A", Address: "addr-a", Enabled: true}
	roomB := roommodel.ClinicRoom{Name: "Room B", Address: "addr-b", Enabled: true}
	if err := db.Create(&roomA).Error; err != nil {
		t.Fatalf("seed room A: %v", err)
	}
	if err := db.Create(&roomB).Error; err != nil {
		t.Fatalf("seed room B: %v", err)
	}

	appt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	full := recordmodel.ClinicRecord{
		User: "u1", Realname: "r1", PhoneNum: "p1",
		Status: recordmodel.RecordStatusCompleted, AppointmentTime: appt,
		QuestionDesc: "broken", RoomID: roomA.ID,
		LiquidMetalStatusIndex: taggingservice.LiquidMetalStatusIndex(taggingservice.LiquidMetalStatusYes),
	}
	sparse := recordmodel.ClinicRecord{
		User: "u2", Realname: "r2", PhoneNum: "p2",
		Status: recordmodel.RecordStatusPending, AppointmentTime: appt.Add(-time.Hour),
		QuestionDesc: "screen", RoomID: roomB.ID,
	}
	if err := db.Create(&full).Error; err != nil {
		t.Fatalf("seed full record: %v", err)
	}
	if err := db.Create(&sparse).Error; err != nil {
		t.Fatalf("seed sparse record: %v", err)
	}
	if err := db.Create(&staffmodel.ClinicStaff{ID: 42, AccountID: "worker42", Realname: "维修员"}).Error; err != nil {
		t.Fatalf("seed worker staff: %v", err)
	}

	arriveTime := appt.Add(time.Hour)
	finishTime := appt.Add(2 * time.Hour)
	sideRows := []any{
		&recordmodel.ClinicRecordDevice{RecordID: full.ID, LaptopModel: "ThinkPad", Password: "pw"},
		&recordmodel.ClinicRecordWorker{RecordID: full.ID, WorkerID: 42, WorkerDesc: "fixed", FinishTime: finishTime},
		&recordmodel.ClinicRecordPhoto{RecordID: full.ID, ObjectKey: "photos/records/1/a.jpg", ContentType: "image/jpeg", Size: 1, Position: 1},
		&recordmodel.ClinicRecordPhoto{RecordID: full.ID, ObjectKey: "photos/records/1/b.jpg", ContentType: "image/jpeg", Size: 1, Position: 2},
		&recordmodel.ClinicRecordRejection{RecordID: full.ID, RejectReason: "nope"},
		&recordmodel.ClinicRecordReferral{RecordID: full.ID, ReferralReason: "elsewhere"},
		&recordmodel.ClinicRecordArrival{RecordID: full.ID, ArriveTime: arriveTime},
	}
	for _, row := range sideRows {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("seed side row %T: %v", row, err)
		}
	}

	views, total, err := svc.List(context.Background(), 0, recordservice.ListAdminRecordFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total 2, got %d", total)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}

	byID := make(map[uint]recordservice.AdminRecordView, len(views))
	for _, v := range views {
		byID[v.ID] = v
	}

	got := byID[full.ID]
	if got.Campus != "Room A" {
		t.Errorf("full campus = %q, want Room A", got.Campus)
	}
	if got.Model != "ThinkPad" || got.Password != "pw" {
		t.Errorf("full device = %q/%q, want ThinkPad/pw", got.Model, got.Password)
	}
	if got.WorkerDesc != "fixed" {
		t.Errorf("full worker_desc = %q, want fixed", got.WorkerDesc)
	}
	if got.PhotoCount != 2 {
		t.Errorf("full photo_count = %d, want 2", got.PhotoCount)
	}
	if got.WorkerID == nil || *got.WorkerID != 42 {
		t.Errorf("full worker_id = %v, want 42", got.WorkerID)
	}
	if got.WorkerRealname != "维修员" {
		t.Errorf("full worker_realname = %q, want 维修员", got.WorkerRealname)
	}
	if want := finishTime.UTC().Format(time.RFC3339); got.FinishTime == nil || *got.FinishTime != want {
		t.Errorf("full finish_time = %v, want %s", got.FinishTime, want)
	}
	if got.RejectReason != "nope" {
		t.Errorf("full reject_reason = %q, want nope", got.RejectReason)
	}
	if got.ReferralReason != "elsewhere" {
		t.Errorf("full referral_reason = %q, want elsewhere", got.ReferralReason)
	}
	if want := arriveTime.UTC().Format(time.RFC3339); got.ArriveTime == nil || *got.ArriveTime != want {
		t.Errorf("full arrive_time = %v, want %s", got.ArriveTime, want)
	}
	if want := appt.UTC().Format("2006-01-02"); got.AppointmentTime != want {
		t.Errorf("full appointment_time = %q, want %s", got.AppointmentTime, want)
	}
	if got.LiquidMetalStatus != string(taggingservice.LiquidMetalStatusYes) {
		t.Errorf("full liquid_metal_status = %q, want %q", got.LiquidMetalStatus, taggingservice.LiquidMetalStatusYes)
	}

	empty := byID[sparse.ID]
	if empty.Campus != "Room B" {
		t.Errorf("sparse campus = %q, want Room B", empty.Campus)
	}
	if empty.Model != "" || empty.Password != "" {
		t.Errorf("sparse device = %q/%q, want empty", empty.Model, empty.Password)
	}
	if empty.WorkerDesc != "" || empty.WorkerID != nil || empty.WorkerRealname != "" || empty.FinishTime != nil {
		t.Errorf("sparse worker = %q/%v/%q/%v, want empty", empty.WorkerDesc, empty.WorkerID, empty.WorkerRealname, empty.FinishTime)
	}
	if empty.PhotoCount != 0 {
		t.Errorf("sparse photo_count = %d, want 0", empty.PhotoCount)
	}
	if empty.RejectReason != "" || empty.ReferralReason != "" || empty.ArriveTime != nil {
		t.Errorf("sparse side fields = %q/%q/%v, want empty", empty.RejectReason, empty.ReferralReason, empty.ArriveTime)
	}
	if empty.LiquidMetalStatus != string(taggingservice.LiquidMetalStatusUnknown) {
		t.Errorf("sparse liquid_metal_status = %q, want %q", empty.LiquidMetalStatus, taggingservice.LiquidMetalStatusUnknown)
	}
}

func TestAdminRecordServiceGetByIDPhotoCount(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)

	room := roommodel.ClinicRoom{Name: "Room", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	rec := recordmodel.ClinicRecord{
		User: "u", Realname: "r", PhoneNum: "p",
		Status:          recordmodel.RecordStatusInProgress,
		AppointmentTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		QuestionDesc:    "x",
		RoomID:          room.ID,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}
	for position := 1; position <= 3; position++ {
		if err := db.Create(&recordmodel.ClinicRecordPhoto{
			RecordID:    rec.ID,
			ObjectKey:   fmt.Sprintf("photos/records/%d/%d.jpg", rec.ID, position),
			ContentType: "image/jpeg",
			Size:        1,
			Position:    position,
		}).Error; err != nil {
			t.Fatalf("seed photo %d: %v", position, err)
		}
	}

	view, err := svc.GetByID(context.Background(), 0, rec.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if view.PhotoCount != 3 {
		t.Errorf("photo_count = %d, want 3", view.PhotoCount)
	}
}

func TestAdminRecordServiceListBatchedQueryCount(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)

	room := roommodel.ClinicRoom{Name: "Room", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	appt := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := db.Create(&recordmodel.ClinicRecord{
			User: "u", Realname: "r", PhoneNum: "p",
			Status:          recordmodel.RecordStatusPending,
			AppointmentTime: appt.Add(time.Duration(i) * time.Hour),
			QuestionDesc:    "x",
			RoomID:          room.ID,
		}).Error; err != nil {
			t.Fatalf("seed record %d: %v", i, err)
		}
	}

	counter := &countingGormLogger{Interface: logger.Default}
	db.Logger = counter

	if _, _, err := svc.List(context.Background(), 0, recordservice.ListAdminRecordFilter{Page: 1, PageSize: 10}); err != nil {
		t.Fatalf("List: %v", err)
	}

	// count + page find + room + device + worker + photo counts + rejection + referral + arrival.
	const want = 9
	if counter.n != want {
		t.Errorf("List issued %d queries, want %d (per-record loads likely regressed)", counter.n, want)
	}
}

func TestAdminRecordServiceListSearchByRealname(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	svc := recordservice.NewAdminRecordService(db)

	room := roommodel.ClinicRoom{Name: "Room", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	appt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	records := []recordmodel.ClinicRecord{
		{ID: 1, Realname: "张三"},
		{ID: 2, Realname: "李四"},
		{ID: 3, Realname: "张三丰"},
		{ID: 4, Realname: "王五"},
		{ID: 5, Realname: "张三妹"},
		{ID: 6, Realname: "Zhang San"},
		{ID: 7, Realname: "100%"},
		{ID: 8, Realname: "100x"},
		{ID: 9, Realname: "a_b"},
		{ID: 10, Realname: "axb"},
	}
	for i := range records {
		records[i].User = "u"
		records[i].PhoneNum = "p"
		records[i].Status = recordmodel.RecordStatusPending
		records[i].AppointmentTime = appt
		records[i].QuestionDesc = "x"
		records[i].RoomID = room.ID
		if err := db.Create(&records[i]).Error; err != nil {
			t.Fatalf("seed record %d: %v", records[i].ID, err)
		}
	}

	listIDs := func(f recordservice.ListAdminRecordFilter) ([]uint, int64) {
		t.Helper()
		views, total, err := svc.List(context.Background(), 0, f)
		if err != nil {
			t.Fatalf("List(%+v): %v", f, err)
		}
		ids := make([]uint, 0, len(views))
		for _, v := range views {
			ids = append(ids, v.ID)
		}
		return ids, total
	}

	// Contains matching is paged newest-ID-first.
	ids, total := listIDs(recordservice.ListAdminRecordFilter{Realname: "张", Page: 1, PageSize: 2})
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(ids) != 2 || ids[0] != 5 || ids[1] != 3 {
		t.Fatalf("page 1 IDs = %v, want [5 3]", ids)
	}
	ids, total = listIDs(recordservice.ListAdminRecordFilter{Realname: "张", Page: 2, PageSize: 2})
	if total != 3 {
		t.Fatalf("page 2 total = %d, want 3", total)
	}
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("page 2 IDs = %v, want [1]", ids)
	}

	// Matching is case-insensitive.
	ids, total = listIDs(recordservice.ListAdminRecordFilter{Realname: "zhang"})
	if total != 1 || len(ids) != 1 || ids[0] != 6 {
		t.Fatalf("case-insensitive search = %v (total %d), want [6] (total 1)", ids, total)
	}

	// A whitespace-only term behaves like no realname filter.
	_, total = listIDs(recordservice.ListAdminRecordFilter{Realname: "   "})
	if total != int64(len(records)) {
		t.Fatalf("whitespace search total = %d, want %d", total, len(records))
	}

	// LIKE metacharacters are matched literally.
	ids, total = listIDs(recordservice.ListAdminRecordFilter{Realname: "%"})
	if total != 1 || len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("percent search = %v (total %d), want [7] (total 1)", ids, total)
	}
	ids, total = listIDs(recordservice.ListAdminRecordFilter{Realname: "_"})
	if total != 1 || len(ids) != 1 || ids[0] != 9 {
		t.Fatalf("underscore search = %v (total %d), want [9] (total 1)", ids, total)
	}
}
