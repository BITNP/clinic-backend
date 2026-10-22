package service_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
	"clinic-backend/internal/testutil"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupServiceDateServiceDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicRoom{}, &schedulingmodel.ClinicServiceDate{}, &recordmodel.ClinicRecord{}); err != nil {
		t.Fatalf("failed to migrate models: %v", err)
	}
	return db
}

func TestServiceDateService_Create(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)
	testutil.MustCreateRoom(t, db, 1)

	d, err := svc.Create(testutil.ValidServiceDateInput(1, 5, 10))
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("expected ID to be assigned")
	}
	if d.Capacity != 10 || d.Title != "Open Clinic" {
		t.Errorf("unexpected service date: %+v", d)
	}
}

func TestServiceDateService_GetByID_NotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	_, err := svc.GetByID(999)
	if !errors.Is(err, schedulingservice.ErrServiceDateNotFound) {
		t.Fatalf("expected ErrServiceDateNotFound, got %v", err)
	}
}

func TestServiceDateService_Create_RoomNotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	_, err := svc.Create(testutil.ValidServiceDateInput(999, 5, 10))
	if !errors.Is(err, schedulingservice.ErrServiceDateRoomNotFound) {
		t.Fatalf("expected ErrServiceDateRoomNotFound, got %v", err)
	}
}

func TestServiceDateService_List_Filter(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	room1 := uint(1)
	room2 := uint(2)
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(room1, -1, 5)) // past
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(room1, 3, 5))
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(room2, 7, 5))

	t.Run("all", func(t *testing.T) {
		items, total, err := svc.List(schedulingservice.ListServiceDateFilter{})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 3 || len(items) != 3 {
			t.Fatalf("expected 3 items, got %d (total %d)", len(items), total)
		}
		// Order should be by date asc, so the past one comes first.
		if !items[0].Date.Before(testutil.FutureDate(0)) {
			t.Errorf("expected first item to be past date, got %v", items[0].Date)
		}
	})

	t.Run("active_only", func(t *testing.T) {
		items, total, err := svc.List(schedulingservice.ListServiceDateFilter{ActiveOnly: true})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 2 || len(items) != 2 {
			t.Fatalf("expected 2 active items, got %d (total %d)", len(items), total)
		}
	})

	t.Run("room_filter", func(t *testing.T) {
		items, total, err := svc.List(schedulingservice.ListServiceDateFilter{RoomID: &room2, ActiveOnly: true})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 1 || len(items) != 1 || *items[0].RoomID != room2 {
			t.Fatalf("expected 1 room2 item, got %d (total %d)", len(items), total)
		}
	})

	t.Run("from_filter", func(t *testing.T) {
		items, total, err := svc.List(schedulingservice.ListServiceDateFilter{FromDate: testutil.FutureDate(4)})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 1 || len(items) != 1 {
			t.Fatalf("expected 1 item on/after day 4, got %d (total %d)", len(items), total)
		}
	})

	t.Run("room_ids_filter", func(t *testing.T) {
		items, total, err := svc.List(schedulingservice.ListServiceDateFilter{RoomIDs: []uint{room1, room2}})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 3 || len(items) != 3 {
			t.Fatalf("expected 3 items for both rooms, got %d (total %d)", len(items), total)
		}
	})

	t.Run("room_ids_filter_overrides_room_id", func(t *testing.T) {
		items, total, err := svc.List(schedulingservice.ListServiceDateFilter{RoomID: &room2, RoomIDs: []uint{room1}})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 2 || len(items) != 2 {
			t.Fatalf("expected 2 items for room1 only, got %d (total %d)", len(items), total)
		}
		for _, it := range items {
			if *it.RoomID != room1 {
				t.Errorf("expected only room1 items, got room %d", *it.RoomID)
			}
		}
	})

	t.Run("to_filter", func(t *testing.T) {
		items, total, err := svc.List(schedulingservice.ListServiceDateFilter{ToDate: testutil.FutureDate(3)})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 2 || len(items) != 2 {
			t.Fatalf("expected 2 items on/before day 3, got %d (total %d)", len(items), total)
		}
	})

	t.Run("range_filter", func(t *testing.T) {
		items, total, err := svc.List(schedulingservice.ListServiceDateFilter{
			FromDate: testutil.FutureDate(0),
			ToDate:   testutil.FutureDate(3),
		})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 1 || len(items) != 1 {
			t.Fatalf("expected 1 item between today and day 3, got %d (total %d)", len(items), total)
		}
		if *items[0].RoomID != room1 {
			t.Errorf("expected the day-3 room1 item, got room %d", *items[0].RoomID)
		}
	})
}

func TestServiceDateService_GetByDateAndRoom_NonMidnightInput(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	loc := time.FixedZone("UTC+8", 8*60*60)
	localDay := time.Date(2026, 7, 20, 0, 0, 0, 0, loc)
	inputDate := localDay.UTC() // what the admin frontend sends for local 7.20

	testutil.MustCreateServiceDate(t, db, svc, schedulingservice.CreateServiceDateInput{
		Capacity:  5,
		RoomID:    1,
		Date:      inputDate,
		StartTime: inputDate,
		EndTime:   inputDate.Add(2 * time.Hour),
		Title:     "Open",
	})

	_, err := svc.GetByDateAndRoom(1, inputDate)
	if err != nil {
		t.Fatalf("lookup with non-midnight input should find stored date: %v", err)
	}
}

func TestServiceDateService_List_ActiveOnly_TodayInTimezone(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	loc := time.FixedZone("UTC+8", 8*60*60)
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterday := today.AddDate(0, 0, -1)
	tomorrow := today.AddDate(0, 0, 1)

	createLocalDate := func(localDay time.Time) {
		inputDate := localDay.UTC() // frontend sends UTC ISO of local midnight
		testutil.MustCreateServiceDate(t, db, svc, schedulingservice.CreateServiceDateInput{
			Capacity:  5,
			RoomID:    1,
			Date:      inputDate,
			StartTime: inputDate,
			EndTime:   inputDate.Add(2 * time.Hour),
			Title:     "Open",
		})
	}

	createLocalDate(yesterday)
	createLocalDate(today)
	createLocalDate(tomorrow)

	items, total, err := svc.List(schedulingservice.ListServiceDateFilter{ActiveOnly: true, TodayLoc: loc})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 active items (today and tomorrow), got %d", total)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestServiceDateService_TicketedCount_ByRecency(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)
	testutil.MustCreateRoom(t, db, 1)

	past := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, -15, 20))
	future := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 15, 20))

	seedStatuses := []recordmodel.RecordStatus{
		recordmodel.RecordStatusPending,
		recordmodel.RecordStatusConfirmed,
		recordmodel.RecordStatusArrived,
		recordmodel.RecordStatusInProgress,
		recordmodel.RecordStatusCompleted,
		recordmodel.RecordStatusRejected,
		recordmodel.RecordStatusReferred,
		recordmodel.RecordStatusNoShow,
	}
	seedForDay := func(date schedulingmodel.ClinicServiceDate, userPrefix string) {
		t.Helper()
		for i, st := range seedStatuses {
			if err := db.Create(&recordmodel.ClinicRecord{
				User:            fmt.Sprintf("%s-%d", userPrefix, i),
				Realname:        "x",
				PhoneNum:        "000",
				Status:          st,
				AppointmentTime: date.Date,
				QuestionDesc:    "x",
				RoomID:          *date.RoomID,
			}).Error; err != nil {
				t.Fatalf("seed record %s: %v", st, err)
			}
		}
	}
	seedForDay(past, "past")
	seedForDay(future, "future")

	cases := []struct {
		name string
		id   uint
		want int64
	}{
		// Today or earlier: rejected/referred/no_show excluded → 5 of 8 count.
		{"past", past.ID, 5},
		// After today: completed is excluded too → 4 of 8 count.
		{"future", future.ID, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.GetByID(tc.id)
			if err != nil {
				t.Fatalf("get by id: %v", err)
			}
			if got.Count != tc.want {
				t.Errorf("expected count %d, got %d", tc.want, got.Count)
			}
		})
	}
}

func TestServiceDateService_List_HasCapacity(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	roomID := uint(1)
	testutil.MustCreateRoom(t, db, roomID)
	d, err := svc.Create(testutil.ValidServiceDateInput(roomID, 5, 2))
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Seed two accepted records to fill the date.
	for i := 0; i < 2; i++ {
		if err := db.Create(&recordmodel.ClinicRecord{
			Realname:        "x",
			PhoneNum:        "000",
			Status:          recordmodel.RecordStatusConfirmed,
			AppointmentTime: d.Date,
			RoomID:          roomID,
		}).Error; err != nil {
			t.Fatalf("seed record failed: %v", err)
		}
	}

	items, total, err := svc.List(schedulingservice.ListServiceDateFilter{ActiveOnly: true, HasCapacity: true})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if total != 1 {
		t.Fatalf("total counts all rows; expected 1, got %d", total)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 available items since full, got %d", len(items))
	}
}

func TestServiceDateService_Update_Partial(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 5, 10))
	newTitle := "Closed Day"
	updated, err := svc.Update(d.ID, schedulingservice.UpdateServiceDateInput{Title: &newTitle})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Title != "Closed Day" || updated.Capacity != 10 {
		t.Errorf("unexpected: %+v", updated)
	}
}

func TestServiceDateService_Update_NotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	newTitle := "x"
	_, err := svc.Update(999, schedulingservice.UpdateServiceDateInput{Title: &newTitle})
	if !errors.Is(err, schedulingservice.ErrServiceDateNotFound) {
		t.Fatalf("expected ErrServiceDateNotFound, got %v", err)
	}
}

func TestServiceDateService_Update_RoomNotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)
	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 5, 10))

	badRoom := uint(999)
	_, err := svc.Update(d.ID, schedulingservice.UpdateServiceDateInput{RoomID: &badRoom})
	if !errors.Is(err, schedulingservice.ErrServiceDateRoomNotFound) {
		t.Fatalf("expected ErrServiceDateRoomNotFound, got %v", err)
	}
}

func TestServiceDateService_Update_InUse(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)
	testutil.MustCreateRoom(t, db, 1)
	testutil.MustCreateRoom(t, db, 2)

	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 5, 10))
	if err := db.Create(&recordmodel.ClinicRecord{
		Realname:        "x",
		PhoneNum:        "000",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: d.Date,
		RoomID:          1,
	}).Error; err != nil {
		t.Fatalf("seed record failed: %v", err)
	}

	// Title changes should be allowed when records exist.
	newTitle := "New Title"
	if _, err := svc.Update(d.ID, schedulingservice.UpdateServiceDateInput{Title: &newTitle}); err != nil {
		t.Fatalf("title update should succeed, got %v", err)
	}

	// Room changes should be blocked when records exist.
	newRoom := uint(2)
	_, err := svc.Update(d.ID, schedulingservice.UpdateServiceDateInput{RoomID: &newRoom})
	if !errors.Is(err, schedulingservice.ErrServiceDateInUse) {
		t.Fatalf("room change should be blocked, expected ErrServiceDateInUse, got %v", err)
	}

	// Sending the unchanged room/date payload with a capacity update should be allowed.
	sameRoom := uint(1)
	sameDate := d.Date
	updatedTitle := "Updated Title"
	newStart := d.StartTime
	newEnd := d.EndTime
	newCap := uint(11)
	if _, err := svc.Update(d.ID, schedulingservice.UpdateServiceDateInput{
		Capacity:  &newCap,
		RoomID:    &sameRoom,
		Date:      &sameDate,
		StartTime: &newStart,
		EndTime:   &newEnd,
		Title:     &updatedTitle,
	}); err != nil {
		t.Fatalf("unchanged room/date payload should succeed, got %v", err)
	}

	// Capacity changes below booked count should be blocked.
	newCap = uint(0)
	_, err = svc.Update(d.ID, schedulingservice.UpdateServiceDateInput{Capacity: &newCap})
	if !errors.Is(err, schedulingservice.ErrServiceDateInUse) {
		t.Fatalf("capacity below booked should be blocked, expected ErrServiceDateInUse, got %v", err)
	}

	// Capacity changes at or above booked count should be allowed.
	newCap = uint(10)
	if _, err = svc.Update(d.ID, schedulingservice.UpdateServiceDateInput{Capacity: &newCap}); err != nil {
		t.Fatalf("capacity >= booked should succeed, got %v", err)
	}
}

func TestServiceDateService_Delete_Success(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 5, 10))
	if err := svc.Delete(d.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := svc.GetByID(d.ID); !errors.Is(err, schedulingservice.ErrServiceDateNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestServiceDateService_Delete_NotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	if err := svc.Delete(999); !errors.Is(err, schedulingservice.ErrServiceDateNotFound) {
		t.Fatalf("expected ErrServiceDateNotFound, got %v", err)
	}
}

func TestServiceDateService_Delete_InUse(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := schedulingservice.NewServiceDateService(db, nil)

	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 5, 10))
	if err := db.Create(&recordmodel.ClinicRecord{
		Realname:        "x",
		PhoneNum:        "000",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: d.Date,
		RoomID:          1,
	}).Error; err != nil {
		t.Fatalf("seed record failed: %v", err)
	}

	if err := svc.Delete(d.ID); !errors.Is(err, schedulingservice.ErrServiceDateInUse) {
		t.Fatalf("expected ErrServiceDateInUse, got %v", err)
	}
}
