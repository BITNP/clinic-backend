package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"clinic-backend/internal/calendar"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	taggingservice "clinic-backend/internal/tagging/service"
	"clinic-backend/internal/testutil"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTicketTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&roommodel.ClinicRoom{},
		&schedulingmodel.ClinicServiceDate{},
		&recordmodel.ClinicRecord{},
		&recordmodel.ClinicRecordDevice{},
		&recordmodel.ClinicRecordArrival{},
		&recordmodel.ClinicRecordWorker{},
		&recordmodel.ClinicRecordPhoto{},
		&recordmodel.ClinicRecordRejection{},
		&recordmodel.ClinicRecordReferral{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestTicketService_Create_HappyPath(t *testing.T) {
	db := setupTicketTestDB(t)
	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)

	rec, err := svc.Create(recordservice.CreateTicketInput{
		User:            "1120221234",
		Realname:        "张三",
		PhoneNum:        "13800138000",
		Campus:          "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7),
		Description:     "笔电不开机",
		Model:           "ThinkPad X1",
		Password:        "pw",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.ID == 0 || rec.Status != recordmodel.RecordStatusPending || rec.User != "1120221234" {
		t.Errorf("unexpected record: %+v", rec)
	}
	var device recordmodel.ClinicRecordDevice
	if err := db.Where("record_id = ?", rec.ID).First(&device).Error; err != nil {
		t.Fatalf("device not saved: %v", err)
	}
	if device.LaptopModel != "ThinkPad X1" || device.Password != "pw" {
		t.Errorf("device mismatch: %+v", device)
	}
}

func TestTicketService_Create_ValidationBranches(t *testing.T) {
	cases := []struct {
		name    string
		seed    func(t *testing.T, db *gorm.DB)
		input   recordservice.CreateTicketInput
		wantErr error
	}{
		{
			name: "step1 date closed (no service date row)",
			seed: func(t *testing.T, db *gorm.DB) {
				testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
			},
			input:   recordservice.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村", AppointmentTime: testutil.FutureTruncatedDate(8), Description: "d"},
			wantErr: recordservice.ErrTicketDateClosed,
		},
		{
			name: "step2 no capacity",
			seed: func(t *testing.T, db *gorm.DB) {
				roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 1)
				existing := recordmodel.ClinicRecord{User: "other", Realname: "x", PhoneNum: "p", Status: recordmodel.RecordStatusPending, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
				if err := db.Create(&existing).Error; err != nil {
					t.Fatalf("seed: %v", err)
				}
			},
			input:   recordservice.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村", AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d"},
			wantErr: recordservice.ErrTicketNoCapacity,
		},
		{
			name: "step3 one working per user",
			seed: func(t *testing.T, db *gorm.DB) {
				roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
				testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(14), 5)
				existing := recordmodel.ClinicRecord{User: "u", Realname: "x", PhoneNum: "p", Status: recordmodel.RecordStatusPending, AppointmentTime: testutil.FutureTruncatedDate(14), QuestionDesc: "x", RoomID: roomID}
				if err := db.Create(&existing).Error; err != nil {
					t.Fatalf("seed: %v", err)
				}
			},
			input:   recordservice.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村", AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d"},
			wantErr: recordservice.ErrTicketOneWorking,
		},
		{
			name: "step4 past appointment time",
			seed: func(t *testing.T, db *gorm.DB) {
				testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(-1), 5)
			},
			input:   recordservice.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村", AppointmentTime: testutil.FutureTruncatedDate(-1), Description: "d"},
			wantErr: recordservice.ErrTicketPastTime,
		},
		{
			name: "campus does not exist",
			seed: func(t *testing.T, db *gorm.DB) {
				testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
			},
			input:   recordservice.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "不存在", AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d"},
			wantErr: recordservice.ErrTicketRoomMissing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTicketTestDB(t)
			tc.seed(t, db)
			svc := recordservice.NewTicketService(db, nil)
			_, err := svc.Create(tc.input)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestTicketService_Create_TodayInTimezone(t *testing.T) {
	db := setupTicketTestDB(t)
	loc := time.FixedZone("UTC+8", 8*60*60)
	svc := recordservice.NewTicketService(db, loc)

	// Simulate a service date created for today's local date in UTC+8.
	now := time.Now().In(loc)
	todayLocal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	inputDate := todayLocal.UTC() // what the customer frontend sends

	roomID := testutil.SeedOpenServiceDate(t, db, loc, inputDate, 5)

	_, err := svc.Create(recordservice.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: inputDate,
		Description:     "d",
	})
	if err != nil {
		t.Fatalf("booking for today in UTC+8 should succeed: %v", err)
	}

	// Booking for yesterday should still be rejected.
	yesterdayLocal := todayLocal.AddDate(0, 0, -1)
	yesterdayInput := yesterdayLocal.UTC()
	testutil.SeedOpenServiceDate(t, db, loc, yesterdayInput, 5)
	_, err = svc.Create(recordservice.CreateTicketInput{
		User:            "u2",
		Realname:        "r2",
		PhoneNum:        "p2",
		Campus:          "中关村",
		AppointmentTime: yesterdayInput,
		Description:     "d",
	})
	if !errors.Is(err, recordservice.ErrTicketPastTime) {
		t.Fatalf("booking for yesterday should be rejected with ErrTicketPastTime, got %v", err)
	}

	// Prevent the yesterday booking from affecting capacity by using a different room.
	_ = roomID
}

func TestTicketService_Create_CapacityExcludesRejected(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 1)
	rejected := recordmodel.ClinicRecord{User: "other", Realname: "x", PhoneNum: "p", Status: recordmodel.RecordStatusRejected, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&rejected).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := recordservice.NewTicketService(db, nil)
	_, err := svc.Create(recordservice.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("expected success (rejected doesn't count), got %v", err)
	}
}

func TestTicketService_Create_CapacityExcludesClosedOnFuture(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 1)
	for _, st := range []recordmodel.RecordStatus{
		recordmodel.RecordStatusCompleted,
		recordmodel.RecordStatusReferred,
		recordmodel.RecordStatusNoShow,
		recordmodel.RecordStatusRejected,
	} {
		r := recordmodel.ClinicRecord{User: "other-" + string(st), Realname: "x", PhoneNum: "p", Status: st, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed %s: %v", st, err)
		}
	}
	svc := recordservice.NewTicketService(db, nil)
	if _, err := svc.Create(recordservice.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
	}); err != nil {
		t.Fatalf("expected success on future date (completed/referred/no_show don't count), got %v", err)
	}
}

func TestTicketService_Create_CapacityCountsCompletedOnToday(t *testing.T) {
	db := setupTicketTestDB(t)
	loc := time.FixedZone("UTC+8", 8*60*60)
	svc := recordservice.NewTicketService(db, loc)

	now := time.Now().In(loc)
	todayLocal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	inputDate := todayLocal.UTC()

	roomID := testutil.SeedOpenServiceDate(t, db, loc, inputDate, 1)
	day := calendar.DateInLocation(inputDate, loc)
	completed := recordmodel.ClinicRecord{User: "other", Realname: "x", PhoneNum: "p", Status: recordmodel.RecordStatusCompleted, AppointmentTime: day, QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&completed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := svc.Create(recordservice.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: inputDate, Description: "d",
	})
	if !errors.Is(err, recordservice.ErrTicketNoCapacity) {
		t.Fatalf("expected ErrTicketNoCapacity (completed counts on today), got %v", err)
	}
}

func TestTicketService_Working(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)

	if rec, err := svc.Working("u"); err != nil || rec != nil {
		t.Fatalf("expected nil/no-err, got rec=%v err=%v", rec, err)
	}

	// Direct inserts bypassing Create to model the invariant-violation case
	// (two working records for one user). Create's Step 3 normally prevents
	// this; the spec requires us to take the first by id and log a warning.
	r1 := recordmodel.ClinicRecord{User: "u", Realname: "a", PhoneNum: "p", Status: recordmodel.RecordStatusPending, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&r1).Error; err != nil {
		t.Fatalf("seed r1: %v", err)
	}
	r2 := recordmodel.ClinicRecord{User: "u", Realname: "b", PhoneNum: "p", Status: recordmodel.RecordStatusInProgress, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "y", RoomID: roomID}
	if err := db.Create(&r2).Error; err != nil {
		t.Fatalf("seed r2: %v", err)
	}

	got, err := svc.Working("u")
	if err != nil {
		t.Fatalf("Working under multi-state: %v", err)
	}
	if got == nil {
		t.Fatalf("expected a working record")
	}
	// Should be the smallest id (oldest).
	want := r1.ID
	if r2.ID < r1.ID {
		want = r2.ID
	}
	if got.ID != want {
		t.Errorf("expected smallest id %d, got %d", want, got.ID)
	}

	// Verify the empty case under a different user.
	if got2, err := svc.Working("nobody"); err != nil || got2 != nil {
		t.Fatalf("expected nil for unknown user, got %v err=%v", got2, err)
	}
}

func TestTicketService_List_HidesExpiredWorking(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(-1), 5)
	svc := recordservice.NewTicketService(db, nil)

	// A pending record whose appointment_time is in the past — should be hidden.
	expired := recordmodel.ClinicRecord{User: "u", Realname: "r", PhoneNum: "p", Status: recordmodel.RecordStatusPending, AppointmentTime: testutil.FutureTruncatedDate(-1), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&expired).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A finished record — should appear.
	finished := recordmodel.ClinicRecord{User: "u", Realname: "r", PhoneNum: "p", Status: recordmodel.RecordStatusCompleted, AppointmentTime: testutil.FutureTruncatedDate(-2), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&finished).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	items, total, err := svc.List("u", recordservice.ListTicketFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != finished.ID {
		t.Errorf("expected only the finished record, got total %d items %+v", total, items)
	}
}

func TestTicketService_Finished_OnlyClosedStatuses(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)

	closed := []recordmodel.RecordStatus{recordmodel.RecordStatusRejected, recordmodel.RecordStatusCompleted, recordmodel.RecordStatusReferred, recordmodel.RecordStatusNoShow}
	open := []recordmodel.RecordStatus{recordmodel.RecordStatusPending, recordmodel.RecordStatusConfirmed, recordmodel.RecordStatusArrived, recordmodel.RecordStatusInProgress}
	for i, st := range closed {
		r := recordmodel.ClinicRecord{User: "u", Realname: "r", PhoneNum: "p", Status: st, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed closed[%d]: %v", i, err)
		}
	}
	for i, st := range open {
		r := recordmodel.ClinicRecord{User: "u", Realname: "r", PhoneNum: "p", Status: st, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed open[%d]: %v", i, err)
		}
	}

	items, total, err := svc.Finished("u", recordservice.ListTicketFilter{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("finished: %v", err)
	}
	if total != int64(len(closed)) || len(items) != len(closed) {
		t.Errorf("expected %d finished, got total %d items %d", len(closed), total, len(items))
	}
}

func TestTicketService_GetForUser_Ownership(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	r := recordmodel.ClinicRecord{User: "alice", Realname: "r", PhoneNum: "p", Status: recordmodel.RecordStatusPending, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := recordservice.NewTicketService(db, nil)
	if _, err := svc.GetForUser(r.ID, "alice"); err != nil {
		t.Errorf("owner get: %v", err)
	}
	if _, err := svc.GetForUser(r.ID, "bob"); !errors.Is(err, recordservice.ErrTicketForbidden) {
		t.Errorf("expected forbidden, got %v", err)
	}
	if _, err := svc.GetForUser(999, "alice"); !errors.Is(err, recordservice.ErrTicketNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
}

func TestTicketService_Delete(t *testing.T) {
	t.Run("ok when service date exists", func(t *testing.T) {
		db := setupTicketTestDB(t)
		roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
		svc := recordservice.NewTicketService(db, nil)
		rec, err := svc.Create(recordservice.CreateTicketInput{
			User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
			AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := svc.DeleteForUser(rec.ID, "u"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		var n int64
		db.Model(&recordmodel.ClinicRecord{}).Where("id = ?", rec.ID).Count(&n)
		if n != 0 {
			t.Errorf("record not deleted")
		}
		// device row should also be gone.
		db.Model(&recordmodel.ClinicRecordDevice{}).Where("record_id = ?", rec.ID).Count(&n)
		if n != 0 {
			t.Errorf("device not deleted")
		}
		_ = roomID
	})

	t.Run("400 when service date row removed", func(t *testing.T) {
		db := setupTicketTestDB(t)
		roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
		svc := recordservice.NewTicketService(db, nil)
		rec, err := svc.Create(recordservice.CreateTicketInput{
			User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
			AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		// Nuke the ServiceDate row out from under the record.
		if err := db.Where("room_id = ? AND date = ?", roomID, testutil.FutureTruncatedDate(7)).Delete(&schedulingmodel.ClinicServiceDate{}).Error; err != nil {
			t.Fatalf("nuke: %v", err)
		}
		if err := svc.DeleteForUser(rec.ID, "u"); !errors.Is(err, recordservice.ErrTicketDateClosed) {
			t.Errorf("expected ErrTicketDateClosed, got %v", err)
		}
	})
}

func TestTicketService_DeleteRemovesPhotos(t *testing.T) {
	db := setupTicketTestDB(t)
	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	ticketSvc := recordservice.NewTicketService(db, nil)
	rec, err := ticketSvc.Create(recordservice.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	store := newFakePhotoStorage()
	photoSvc, err := recordservice.NewPhotoService(db, store, 9, 1024)
	if err != nil {
		t.Fatalf("NewPhotoService: %v", err)
	}
	first, err := photoSvc.Append(context.Background(), rec.ID, photoUpload(testJPEG))
	if err != nil {
		t.Fatalf("append first photo: %v", err)
	}
	second, err := photoSvc.Append(context.Background(), rec.ID, photoUpload(testPNG))
	if err != nil {
		t.Fatalf("append second photo: %v", err)
	}
	ticketSvc.SetPhotoService(photoSvc)

	if err := ticketSvc.DeleteForUser(rec.ID, "u"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int64
	db.Model(&recordmodel.ClinicRecord{}).Where("id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Errorf("record not deleted")
	}
	db.Model(&recordmodel.ClinicRecordPhoto{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Errorf("photo rows not deleted: %d", count)
	}
	if len(store.objects) != 0 {
		t.Errorf("photo objects not deleted: %d", len(store.objects))
	}
	if !containsString(store.deleted, first.ObjectKey) || !containsString(store.deleted, second.ObjectKey) {
		t.Errorf("deleted keys = %v, want both photo keys", store.deleted)
	}
}

func TestTicketService_DeleteWithoutPhotos(t *testing.T) {
	db := setupTicketTestDB(t)
	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	ticketSvc := recordservice.NewTicketService(db, nil)
	rec, err := ticketSvc.Create(recordservice.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	photoSvc, err := recordservice.NewPhotoService(db, newFakePhotoStorage(), 9, 1024)
	if err != nil {
		t.Fatalf("NewPhotoService: %v", err)
	}
	ticketSvc.SetPhotoService(photoSvc)

	if err := ticketSvc.DeleteForUser(rec.ID, "u"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int64
	db.Model(&recordmodel.ClinicRecord{}).Where("id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Errorf("record not deleted")
	}
	db.Model(&recordmodel.ClinicRecordPhoto{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Errorf("photo rows = %d, want 0", count)
	}
}

func TestTicketService_DeleteToleratesStorageCleanupFailure(t *testing.T) {
	db := setupTicketTestDB(t)
	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	ticketSvc := recordservice.NewTicketService(db, nil)
	rec, err := ticketSvc.Create(recordservice.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	store := newFakePhotoStorage()
	photoSvc, err := recordservice.NewPhotoService(db, store, 9, 1024)
	if err != nil {
		t.Fatalf("NewPhotoService: %v", err)
	}
	if _, err := photoSvc.Append(context.Background(), rec.ID, photoUpload(testJPEG)); err != nil {
		t.Fatalf("append photo: %v", err)
	}
	store.deleteErr = errors.New("storage unavailable")
	ticketSvc.SetPhotoService(photoSvc)

	if err := ticketSvc.DeleteForUser(rec.ID, "u"); err != nil {
		t.Fatalf("delete should succeed despite storage cleanup failure: %v", err)
	}

	var count int64
	db.Model(&recordmodel.ClinicRecord{}).Where("id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Errorf("record not deleted")
	}
	db.Model(&recordmodel.ClinicRecordPhoto{}).Where("record_id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Errorf("photo rows not deleted: %d", count)
	}
	if len(store.objects) != 1 {
		t.Errorf("stored objects = %d, want the failed object to remain", len(store.objects))
	}
}

func TestTicketService_DeleteToleratesPhotoRowCleanupFailure(t *testing.T) {
	db := setupTicketTestDB(t)
	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	ticketSvc := recordservice.NewTicketService(db, nil)
	rec, err := ticketSvc.Create(recordservice.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	store := newFakePhotoStorage()
	photoSvc, err := recordservice.NewPhotoService(db, store, 9, 1024)
	if err != nil {
		t.Fatalf("NewPhotoService: %v", err)
	}
	if _, err := photoSvc.Append(context.Background(), rec.ID, photoUpload(testJPEG)); err != nil {
		t.Fatalf("append photo: %v", err)
	}
	if err := db.Migrator().DropTable(&recordmodel.ClinicRecordPhoto{}); err != nil {
		t.Fatalf("drop photo table: %v", err)
	}
	ticketSvc.SetPhotoService(photoSvc)

	if err := ticketSvc.DeleteForUser(rec.ID, "u"); err != nil {
		t.Fatalf("delete should succeed despite photo row cleanup failure: %v", err)
	}

	var count int64
	db.Model(&recordmodel.ClinicRecord{}).Where("id = ?", rec.ID).Count(&count)
	if count != 0 {
		t.Errorf("record not deleted")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestTicketService_View_Shape(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)
	rec, err := svc.Create(recordservice.CreateTicketInput{
		User: "1120221234", Realname: "张三", PhoneNum: "13800138000", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "笔电不开机",
		Model: "ThinkPad X1", Password: "pw",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rec.LiquidMetalStatusIndex = taggingservice.LiquidMetalStatusIndex(taggingservice.LiquidMetalStatusYes)
	v, err := svc.View(rec)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if v.URL != "/api/tickets/"+testutil.Itoa(rec.ID) {
		t.Errorf("url: %s", v.URL)
	}
	if v.User != "1120221234" || v.Realname != "张三" || v.PhoneNum != "13800138000" {
		t.Errorf("identity fields: %+v", v)
	}
	if v.Campus != "中关村" {
		t.Errorf("campus: %s", v.Campus)
	}
	if v.AppointmentTime != testutil.FutureTruncatedDate(7).Format("2006-01-02") {
		t.Errorf("appointmentTime: %s", v.AppointmentTime)
	}
	if v.Description != "笔电不开机" || v.Model != "ThinkPad X1" || v.Password != "pw" {
		t.Errorf("device/desc fields: %+v", v)
	}
	if v.LiquidMetalStatus != string(taggingservice.LiquidMetalStatusYes) {
		t.Errorf("liquid metal status = %q, want %q", v.LiquidMetalStatus, taggingservice.LiquidMetalStatusYes)
	}
	_ = roomID
}
