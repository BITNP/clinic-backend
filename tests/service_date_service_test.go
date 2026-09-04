package tests

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"clinic-backend/models"
	"clinic-backend/services"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupServiceDateServiceDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&models.ClinicRoom{}, &models.ClinicServiceDate{}, &models.ClinicRecord{}); err != nil {
		t.Fatalf("failed to migrate models: %v", err)
	}
	return db
}

func mustCreateRoom(t *testing.T, db *gorm.DB, id uint) {
	t.Helper()
	room := models.ClinicRoom{ID: id, Address: "A", Enabled: true}
	room.Name = fmt.Sprintf("R-%d", id)
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room failed: %v", err)
	}
}

func futureDateTime(daysFromNow int, hour, minute int) time.Time {
	d := futureDate(daysFromNow)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, time.UTC)
}

func validServiceDateInput(roomID uint, daysFromNow int, cap uint) services.CreateServiceDateInput {
	return services.CreateServiceDateInput{
		Capacity:  cap,
		RoomID:    roomID,
		Date:      futureDate(daysFromNow),
		StartTime: futureDateTime(daysFromNow, 9, 0),
		EndTime:   futureDateTime(daysFromNow, 12, 0),
		Title:     "Open Clinic",
	}
}

func TestServiceDateService_Create(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)
	mustCreateRoom(t, db, 1)

	d, err := svc.Create(validServiceDateInput(1, 5, 10))
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
	svc := services.NewServiceDateService(db, nil)

	_, err := svc.GetByID(999)
	if !errors.Is(err, services.ErrServiceDateNotFound) {
		t.Fatalf("expected ErrServiceDateNotFound, got %v", err)
	}
}

func TestServiceDateService_Create_RoomNotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)

	_, err := svc.Create(validServiceDateInput(999, 5, 10))
	if !errors.Is(err, services.ErrServiceDateRoomNotFound) {
		t.Fatalf("expected ErrServiceDateRoomNotFound, got %v", err)
	}
}

func TestServiceDateService_List_Filter(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)

	room1 := uint(1)
	room2 := uint(2)
	mustCreateServiceDate(t, db, svc, validServiceDateInput(room1, -1, 5)) // past
	mustCreateServiceDate(t, db, svc, validServiceDateInput(room1, 3, 5))
	mustCreateServiceDate(t, db, svc, validServiceDateInput(room2, 7, 5))

	t.Run("all", func(t *testing.T) {
		items, total, err := svc.List(services.ListServiceDateFilter{})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 3 || len(items) != 3 {
			t.Fatalf("expected 3 items, got %d (total %d)", len(items), total)
		}
		// Order should be by date asc, so the past one comes first.
		if !items[0].Date.Before(futureDate(0)) {
			t.Errorf("expected first item to be past date, got %v", items[0].Date)
		}
	})

	t.Run("active_only", func(t *testing.T) {
		items, total, err := svc.List(services.ListServiceDateFilter{ActiveOnly: true})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 2 || len(items) != 2 {
			t.Fatalf("expected 2 active items, got %d (total %d)", len(items), total)
		}
	})

	t.Run("room_filter", func(t *testing.T) {
		items, total, err := svc.List(services.ListServiceDateFilter{RoomID: &room2, ActiveOnly: true})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 1 || len(items) != 1 || *items[0].RoomID != room2 {
			t.Fatalf("expected 1 room2 item, got %d (total %d)", len(items), total)
		}
	})

	t.Run("from_filter", func(t *testing.T) {
		items, total, err := svc.List(services.ListServiceDateFilter{FromDate: futureDate(4)})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 1 || len(items) != 1 {
			t.Fatalf("expected 1 item on/after day 4, got %d (total %d)", len(items), total)
		}
	})

	t.Run("room_ids_filter", func(t *testing.T) {
		items, total, err := svc.List(services.ListServiceDateFilter{RoomIDs: []uint{room1, room2}})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 3 || len(items) != 3 {
			t.Fatalf("expected 3 items for both rooms, got %d (total %d)", len(items), total)
		}
	})

	t.Run("room_ids_filter_overrides_room_id", func(t *testing.T) {
		items, total, err := svc.List(services.ListServiceDateFilter{RoomID: &room2, RoomIDs: []uint{room1}})
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
		items, total, err := svc.List(services.ListServiceDateFilter{ToDate: futureDate(3)})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 2 || len(items) != 2 {
			t.Fatalf("expected 2 items on/before day 3, got %d (total %d)", len(items), total)
		}
	})

	t.Run("range_filter", func(t *testing.T) {
		items, total, err := svc.List(services.ListServiceDateFilter{
			FromDate: futureDate(0),
			ToDate:   futureDate(3),
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
	svc := services.NewServiceDateService(db, nil)

	loc := time.FixedZone("UTC+8", 8*60*60)
	localDay := time.Date(2026, 7, 20, 0, 0, 0, 0, loc)
	inputDate := localDay.UTC() // what the admin frontend sends for local 7.20

	mustCreateServiceDate(t, db, svc, services.CreateServiceDateInput{
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
	svc := services.NewServiceDateService(db, nil)

	loc := time.FixedZone("UTC+8", 8*60*60)
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterday := today.AddDate(0, 0, -1)
	tomorrow := today.AddDate(0, 0, 1)

	createLocalDate := func(localDay time.Time) {
		inputDate := localDay.UTC() // frontend sends UTC ISO of local midnight
		mustCreateServiceDate(t, db, svc, services.CreateServiceDateInput{
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

	items, total, err := svc.List(services.ListServiceDateFilter{ActiveOnly: true, TodayLoc: loc})
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
	svc := services.NewServiceDateService(db, nil)
	mustCreateRoom(t, db, 1)

	past := mustCreateServiceDate(t, db, svc, validServiceDateInput(1, -15, 20))
	future := mustCreateServiceDate(t, db, svc, validServiceDateInput(1, 15, 20))

	seedStatuses := []models.RecordStatus{
		models.RecordStatusPending,
		models.RecordStatusConfirmed,
		models.RecordStatusArrived,
		models.RecordStatusInProgress,
		models.RecordStatusCompleted,
		models.RecordStatusRejected,
		models.RecordStatusReferred,
		models.RecordStatusNoShow,
	}
	seedForDay := func(date models.ClinicServiceDate, userPrefix string) {
		t.Helper()
		for i, st := range seedStatuses {
			if err := db.Create(&models.ClinicRecord{
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
	svc := services.NewServiceDateService(db, nil)

	roomID := uint(1)
	mustCreateRoom(t, db, roomID)
	d, err := svc.Create(validServiceDateInput(roomID, 5, 2))
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Seed two accepted records to fill the date.
	for i := 0; i < 2; i++ {
		if err := db.Create(&models.ClinicRecord{
			Realname:        "x",
			PhoneNum:        "000",
			Status:          models.RecordStatusConfirmed,
			AppointmentTime: d.Date,
			RoomID:          roomID,
		}).Error; err != nil {
			t.Fatalf("seed record failed: %v", err)
		}
	}

	items, total, err := svc.List(services.ListServiceDateFilter{ActiveOnly: true, HasCapacity: true})
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
	svc := services.NewServiceDateService(db, nil)

	d := mustCreateServiceDate(t, db, svc, validServiceDateInput(1, 5, 10))
	newTitle := "Closed Day"
	updated, err := svc.Update(d.ID, services.UpdateServiceDateInput{Title: &newTitle})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Title != "Closed Day" || updated.Capacity != 10 {
		t.Errorf("unexpected: %+v", updated)
	}
}

func TestServiceDateService_Update_NotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)

	newTitle := "x"
	_, err := svc.Update(999, services.UpdateServiceDateInput{Title: &newTitle})
	if !errors.Is(err, services.ErrServiceDateNotFound) {
		t.Fatalf("expected ErrServiceDateNotFound, got %v", err)
	}
}

func TestServiceDateService_Update_RoomNotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)
	d := mustCreateServiceDate(t, db, svc, validServiceDateInput(1, 5, 10))

	badRoom := uint(999)
	_, err := svc.Update(d.ID, services.UpdateServiceDateInput{RoomID: &badRoom})
	if !errors.Is(err, services.ErrServiceDateRoomNotFound) {
		t.Fatalf("expected ErrServiceDateRoomNotFound, got %v", err)
	}
}

func TestServiceDateService_Update_InUse(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)
	mustCreateRoom(t, db, 1)
	mustCreateRoom(t, db, 2)

	d := mustCreateServiceDate(t, db, svc, validServiceDateInput(1, 5, 10))
	if err := db.Create(&models.ClinicRecord{
		Realname:        "x",
		PhoneNum:        "000",
		Status:          models.RecordStatusPending,
		AppointmentTime: d.Date,
		RoomID:          1,
	}).Error; err != nil {
		t.Fatalf("seed record failed: %v", err)
	}

	// Title changes should be allowed when records exist.
	newTitle := "New Title"
	if _, err := svc.Update(d.ID, services.UpdateServiceDateInput{Title: &newTitle}); err != nil {
		t.Fatalf("title update should succeed, got %v", err)
	}

	// Room changes should be blocked when records exist.
	newRoom := uint(2)
	_, err := svc.Update(d.ID, services.UpdateServiceDateInput{RoomID: &newRoom})
	if !errors.Is(err, services.ErrServiceDateInUse) {
		t.Fatalf("room change should be blocked, expected ErrServiceDateInUse, got %v", err)
	}

	// Sending the unchanged room/date payload with a capacity update should be allowed.
	sameRoom := uint(1)
	sameDate := d.Date
	updatedTitle := "Updated Title"
	newStart := d.StartTime
	newEnd := d.EndTime
	newCap := uint(11)
	if _, err := svc.Update(d.ID, services.UpdateServiceDateInput{
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
	_, err = svc.Update(d.ID, services.UpdateServiceDateInput{Capacity: &newCap})
	if !errors.Is(err, services.ErrServiceDateInUse) {
		t.Fatalf("capacity below booked should be blocked, expected ErrServiceDateInUse, got %v", err)
	}

	// Capacity changes at or above booked count should be allowed.
	newCap = uint(10)
	if _, err = svc.Update(d.ID, services.UpdateServiceDateInput{Capacity: &newCap}); err != nil {
		t.Fatalf("capacity >= booked should succeed, got %v", err)
	}
}

func TestServiceDateService_Delete_Success(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)

	d := mustCreateServiceDate(t, db, svc, validServiceDateInput(1, 5, 10))
	if err := svc.Delete(d.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := svc.GetByID(d.ID); !errors.Is(err, services.ErrServiceDateNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestServiceDateService_Delete_NotFound(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)

	if err := svc.Delete(999); !errors.Is(err, services.ErrServiceDateNotFound) {
		t.Fatalf("expected ErrServiceDateNotFound, got %v", err)
	}
}

func TestServiceDateService_Delete_InUse(t *testing.T) {
	db := setupServiceDateServiceDB(t)
	svc := services.NewServiceDateService(db, nil)

	d := mustCreateServiceDate(t, db, svc, validServiceDateInput(1, 5, 10))
	if err := db.Create(&models.ClinicRecord{
		Realname:        "x",
		PhoneNum:        "000",
		Status:          models.RecordStatusPending,
		AppointmentTime: d.Date,
		RoomID:          1,
	}).Error; err != nil {
		t.Fatalf("seed record failed: %v", err)
	}

	if err := svc.Delete(d.ID); !errors.Is(err, services.ErrServiceDateInUse) {
		t.Fatalf("expected ErrServiceDateInUse, got %v", err)
	}
}

func mustCreateServiceDate(t *testing.T, db *gorm.DB, svc *services.ServiceDateService, in services.CreateServiceDateInput) models.ClinicServiceDate {
	t.Helper()
	mustEnsureRoom(t, db, in.RoomID)
	d, err := svc.Create(in)
	if err != nil {
		t.Fatalf("seed service date create failed: %v", err)
	}
	return d
}

// mustEnsureRoom inserts a room with the given id if it does not already exist.
func mustEnsureRoom(t *testing.T, db *gorm.DB, roomID uint) {
	t.Helper()
	var n int64
	if err := db.Model(&models.ClinicRoom{}).Where("id = ?", roomID).Count(&n).Error; err != nil {
		t.Fatalf("count room %d: %v", roomID, err)
	}
	if n == 0 {
		mustCreateRoom(t, db, roomID)
	}
}
