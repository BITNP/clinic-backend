package tests

import (
	"errors"
	"testing"
	"time"

	"clinic-backend/models"
	"clinic-backend/services"

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
		&models.ClinicRoom{},
		&models.ClinicServiceDate{},
		&models.ClinicRecord{},
		&models.ClinicRecordDevice{},
		&models.ClinicRecordArrival{},
		&models.ClinicRecordWorker{},
		&models.ClinicRecordRejection{},
		&models.ClinicRecordReferral{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedOpenServiceDate inserts (idempotently) a Room named "中关村" and a
// ServiceDate on `date` with the given capacity. The date is normalized using
// the same timezone-aware rule as the service layer so that lookup keys match.
// Returns the room id.
func seedOpenServiceDate(t *testing.T, db *gorm.DB, loc *time.Location, date time.Time, capacity uint) uint {
	t.Helper()
	var room models.ClinicRoom
	if err := db.Where("name = ?", "中关村").First(&room).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("lookup room: %v", err)
		}
		room = models.ClinicRoom{Name: "中关村", Address: "addr", Enabled: true}
		if err := db.Create(&room).Error; err != nil {
			t.Fatalf("seed room: %v", err)
		}
	}
	d := models.ClinicServiceDate{
		Capacity:  capacity,
		RoomID:    &room.ID,
		Date:      services.DateInLocation(date, loc),
		StartTime: date.Add(9 * time.Hour),
		EndTime:   date.Add(17 * time.Hour),
		Title:     "open",
	}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("seed service date: %v", err)
	}
	return room.ID
}

func futureTruncatedDate(days int) time.Time {
	return time.Now().UTC().AddDate(0, 0, days).Truncate(24 * time.Hour)
}

func TestTicketService_Create_HappyPath(t *testing.T) {
	db := setupTicketTestDB(t)
	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	svc := services.NewTicketService(db, nil)

	rec, err := svc.Create(services.CreateTicketInput{
		User:            "1120221234",
		Realname:        "张三",
		PhoneNum:        "13800138000",
		Campus:          "中关村",
		AppointmentTime: futureTruncatedDate(7),
		Description:     "笔电不开机",
		Model:           "ThinkPad X1",
		Password:        "pw",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.ID == 0 || rec.Status != models.RecordStatusPending || rec.User != "1120221234" {
		t.Errorf("unexpected record: %+v", rec)
	}
	var device models.ClinicRecordDevice
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
		input   services.CreateTicketInput
		wantErr error
	}{
		{
			name:    "step1 date closed (no service date row)",
			seed:    func(t *testing.T, db *gorm.DB) { seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5) },
			input:   services.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村", AppointmentTime: futureTruncatedDate(8), Description: "d"},
			wantErr: services.ErrTicketDateClosed,
		},
		{
			name: "step2 no capacity",
			seed: func(t *testing.T, db *gorm.DB) {
				roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 1)
				existing := models.ClinicRecord{User: "other", Realname: "x", PhoneNum: "p", Status: models.RecordStatusPending, AppointmentTime: futureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
				if err := db.Create(&existing).Error; err != nil {
					t.Fatalf("seed: %v", err)
				}
			},
			input:   services.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村", AppointmentTime: futureTruncatedDate(7), Description: "d"},
			wantErr: services.ErrTicketNoCapacity,
		},
		{
			name: "step3 one working per user",
			seed: func(t *testing.T, db *gorm.DB) {
				roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
				seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(14), 5)
				existing := models.ClinicRecord{User: "u", Realname: "x", PhoneNum: "p", Status: models.RecordStatusPending, AppointmentTime: futureTruncatedDate(14), QuestionDesc: "x", RoomID: roomID}
				if err := db.Create(&existing).Error; err != nil {
					t.Fatalf("seed: %v", err)
				}
			},
			input:   services.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村", AppointmentTime: futureTruncatedDate(7), Description: "d"},
			wantErr: services.ErrTicketOneWorking,
		},
		{
			name:    "step4 past appointment time",
			seed:    func(t *testing.T, db *gorm.DB) { seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(-1), 5) },
			input:   services.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村", AppointmentTime: futureTruncatedDate(-1), Description: "d"},
			wantErr: services.ErrTicketPastTime,
		},
		{
			name:    "campus does not exist",
			seed:    func(t *testing.T, db *gorm.DB) { seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5) },
			input:   services.CreateTicketInput{User: "u", Realname: "r", PhoneNum: "p", Campus: "不存在", AppointmentTime: futureTruncatedDate(7), Description: "d"},
			wantErr: services.ErrTicketRoomMissing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTicketTestDB(t)
			tc.seed(t, db)
			svc := services.NewTicketService(db, nil)
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
	svc := services.NewTicketService(db, loc)

	// Simulate a service date created for today's local date in UTC+8.
	now := time.Now().In(loc)
	todayLocal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	inputDate := todayLocal.UTC() // what the customer frontend sends

	roomID := seedOpenServiceDate(t, db, loc, inputDate, 5)

	_, err := svc.Create(services.CreateTicketInput{
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
	seedOpenServiceDate(t, db, loc, yesterdayInput, 5)
	_, err = svc.Create(services.CreateTicketInput{
		User:            "u2",
		Realname:        "r2",
		PhoneNum:        "p2",
		Campus:          "中关村",
		AppointmentTime: yesterdayInput,
		Description:     "d",
	})
	if !errors.Is(err, services.ErrTicketPastTime) {
		t.Fatalf("booking for yesterday should be rejected with ErrTicketPastTime, got %v", err)
	}

	// Prevent the yesterday booking from affecting capacity by using a different room.
	_ = roomID
}

func TestTicketService_Create_CapacityExcludesRejected(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 1)
	rejected := models.ClinicRecord{User: "other", Realname: "x", PhoneNum: "p", Status: models.RecordStatusRejected, AppointmentTime: futureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&rejected).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := services.NewTicketService(db, nil)
	_, err := svc.Create(services.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("expected success (rejected doesn't count), got %v", err)
	}
}

func TestTicketService_Create_CapacityExcludesClosedOnFuture(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 1)
	for _, st := range []models.RecordStatus{
		models.RecordStatusCompleted,
		models.RecordStatusReferred,
		models.RecordStatusNoShow,
		models.RecordStatusRejected,
	} {
		r := models.ClinicRecord{User: "other-" + string(st), Realname: "x", PhoneNum: "p", Status: st, AppointmentTime: futureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed %s: %v", st, err)
		}
	}
	svc := services.NewTicketService(db, nil)
	if _, err := svc.Create(services.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "d",
	}); err != nil {
		t.Fatalf("expected success on future date (completed/referred/no_show don't count), got %v", err)
	}
}

func TestTicketService_Create_CapacityCountsCompletedOnToday(t *testing.T) {
	db := setupTicketTestDB(t)
	loc := time.FixedZone("UTC+8", 8*60*60)
	svc := services.NewTicketService(db, loc)

	now := time.Now().In(loc)
	todayLocal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	inputDate := todayLocal.UTC()

	roomID := seedOpenServiceDate(t, db, loc, inputDate, 1)
	day := services.DateInLocation(inputDate, loc)
	completed := models.ClinicRecord{User: "other", Realname: "x", PhoneNum: "p", Status: models.RecordStatusCompleted, AppointmentTime: day, QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&completed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := svc.Create(services.CreateTicketInput{
		User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: inputDate, Description: "d",
	})
	if !errors.Is(err, services.ErrTicketNoCapacity) {
		t.Fatalf("expected ErrTicketNoCapacity (completed counts on today), got %v", err)
	}
}

func TestTicketService_Working(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	svc := services.NewTicketService(db, nil)

	if rec, err := svc.Working("u"); err != nil || rec != nil {
		t.Fatalf("expected nil/no-err, got rec=%v err=%v", rec, err)
	}

	// Direct inserts bypassing Create to model the invariant-violation case
	// (two working records for one user). Create's Step 3 normally prevents
	// this; the spec requires us to take the first by id and log a warning.
	r1 := models.ClinicRecord{User: "u", Realname: "a", PhoneNum: "p", Status: models.RecordStatusPending, AppointmentTime: futureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&r1).Error; err != nil {
		t.Fatalf("seed r1: %v", err)
	}
	r2 := models.ClinicRecord{User: "u", Realname: "b", PhoneNum: "p", Status: models.RecordStatusInProgress, AppointmentTime: futureTruncatedDate(7), QuestionDesc: "y", RoomID: roomID}
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
	roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(-1), 5)
	svc := services.NewTicketService(db, nil)

	// A pending record whose appointment_time is in the past — should be hidden.
	expired := models.ClinicRecord{User: "u", Realname: "r", PhoneNum: "p", Status: models.RecordStatusPending, AppointmentTime: futureTruncatedDate(-1), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&expired).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A finished record — should appear.
	finished := models.ClinicRecord{User: "u", Realname: "r", PhoneNum: "p", Status: models.RecordStatusCompleted, AppointmentTime: futureTruncatedDate(-2), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&finished).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	items, total, err := svc.List("u", services.ListTicketFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != finished.ID {
		t.Errorf("expected only the finished record, got total %d items %+v", total, items)
	}
}

func TestTicketService_Finished_OnlyClosedStatuses(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	svc := services.NewTicketService(db, nil)

	closed := []models.RecordStatus{models.RecordStatusRejected, models.RecordStatusCompleted, models.RecordStatusReferred, models.RecordStatusNoShow}
	open := []models.RecordStatus{models.RecordStatusPending, models.RecordStatusConfirmed, models.RecordStatusArrived, models.RecordStatusInProgress}
	for i, st := range closed {
		r := models.ClinicRecord{User: "u", Realname: "r", PhoneNum: "p", Status: st, AppointmentTime: futureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed closed[%d]: %v", i, err)
		}
	}
	for i, st := range open {
		r := models.ClinicRecord{User: "u", Realname: "r", PhoneNum: "p", Status: st, AppointmentTime: futureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed open[%d]: %v", i, err)
		}
	}

	items, total, err := svc.Finished("u", services.ListTicketFilter{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("finished: %v", err)
	}
	if total != int64(len(closed)) || len(items) != len(closed) {
		t.Errorf("expected %d finished, got total %d items %d", len(closed), total, len(items))
	}
}

func TestTicketService_GetForUser_Ownership(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	r := models.ClinicRecord{User: "alice", Realname: "r", PhoneNum: "p", Status: models.RecordStatusPending, AppointmentTime: futureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := services.NewTicketService(db, nil)
	if _, err := svc.GetForUser(r.ID, "alice"); err != nil {
		t.Errorf("owner get: %v", err)
	}
	if _, err := svc.GetForUser(r.ID, "bob"); !errors.Is(err, services.ErrTicketForbidden) {
		t.Errorf("expected forbidden, got %v", err)
	}
	if _, err := svc.GetForUser(999, "alice"); !errors.Is(err, services.ErrTicketNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
}

func TestTicketService_Delete(t *testing.T) {
	t.Run("ok when service date exists", func(t *testing.T) {
		db := setupTicketTestDB(t)
		roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
		svc := services.NewTicketService(db, nil)
		rec, err := svc.Create(services.CreateTicketInput{
			User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
			AppointmentTime: futureTruncatedDate(7), Description: "d",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := svc.DeleteForUser(rec.ID, "u"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		var n int64
		db.Model(&models.ClinicRecord{}).Where("id = ?", rec.ID).Count(&n)
		if n != 0 {
			t.Errorf("record not deleted")
		}
		// device row should also be gone.
		db.Model(&models.ClinicRecordDevice{}).Where("record_id = ?", rec.ID).Count(&n)
		if n != 0 {
			t.Errorf("device not deleted")
		}
		_ = roomID
	})

	t.Run("400 when service date row removed", func(t *testing.T) {
		db := setupTicketTestDB(t)
		roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
		svc := services.NewTicketService(db, nil)
		rec, err := svc.Create(services.CreateTicketInput{
			User: "u", Realname: "r", PhoneNum: "p", Campus: "中关村",
			AppointmentTime: futureTruncatedDate(7), Description: "d",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		// Nuke the ServiceDate row out from under the record.
		if err := db.Where("room_id = ? AND date = ?", roomID, futureTruncatedDate(7)).Delete(&models.ClinicServiceDate{}).Error; err != nil {
			t.Fatalf("nuke: %v", err)
		}
		if err := svc.DeleteForUser(rec.ID, "u"); !errors.Is(err, services.ErrTicketDateClosed) {
			t.Errorf("expected ErrTicketDateClosed, got %v", err)
		}
	})
}

func TestTicketService_View_Shape(t *testing.T) {
	db := setupTicketTestDB(t)
	roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	svc := services.NewTicketService(db, nil)
	rec, err := svc.Create(services.CreateTicketInput{
		User: "1120221234", Realname: "张三", PhoneNum: "13800138000", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "笔电不开机",
		Model: "ThinkPad X1", Password: "pw",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	v, err := svc.View(rec)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if v.URL != "/api/tickets/"+itoa(rec.ID) {
		t.Errorf("url: %s", v.URL)
	}
	if v.User != "1120221234" || v.Realname != "张三" || v.PhoneNum != "13800138000" {
		t.Errorf("identity fields: %+v", v)
	}
	if v.Campus != "中关村" {
		t.Errorf("campus: %s", v.Campus)
	}
	if v.AppointmentTime != futureTruncatedDate(7).Format("2006-01-02") {
		t.Errorf("appointmentTime: %s", v.AppointmentTime)
	}
	if v.Description != "笔电不开机" || v.Model != "ThinkPad X1" || v.Password != "pw" {
		t.Errorf("device/desc fields: %+v", v)
	}
	_ = roomID
}
