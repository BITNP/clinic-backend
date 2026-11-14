package service_test

import (
	"errors"
	"testing"

	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"
	schedulingmodel "clinic-backend/internal/scheduling/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRoomServiceDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &roommodel.ClinicRoom{}, &schedulingmodel.ClinicServiceDate{}, &recordmodel.ClinicRecord{}); err != nil {
		t.Fatalf("failed to migrate models: %v", err)
	}
	return db
}

func boolPtr(v bool) *bool { return &v }

func strPtr(v string) *string { return &v }

func testCampusID(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	var campus roommodel.ClinicCampus
	if err := db.Where("name = ?", "Test Campus").
		FirstOrCreate(&campus, roommodel.ClinicCampus{Name: "Test Campus"}).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	return campus.ID
}

func createRoom(t *testing.T, db *gorm.DB, roomSvc *roomservice.RoomService, in roomservice.CreateRoomInput) (roommodel.ClinicRoom, error) {
	t.Helper()
	if in.CampusID == 0 {
		in.CampusID = testCampusID(t, db)
	}
	return roomSvc.Create(in)
}

func TestRoomService_Create_Defaults(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{
		Name:    "Main Campus Clinic",
		Address: "123 University Ave",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("expected ID assigned")
	}
	if !r.Enabled {
		t.Errorf("expected Enabled to default to true, got %v", r.Enabled)
	}
	if r.Type != roommodel.RoomTypeNormal {
		t.Errorf("expected Type to default to normal, got %q", r.Type)
	}
	if r.CampusID == nil || r.Campus == nil {
		t.Fatalf("expected campus to be set, got campus_id=%v campus=%+v", r.CampusID, r.Campus)
	}
}

func TestRoomService_Create_RequiresCampus(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	_, err := svc.Create(roomservice.CreateRoomInput{Name: "No Campus", Address: "x"})
	if !errors.Is(err, roomservice.ErrRoomCampusRequired) {
		t.Fatalf("expected ErrRoomCampusRequired, got %v", err)
	}
}

func TestRoomService_Create_CampusNotFound(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	_, err := svc.Create(roomservice.CreateRoomInput{Name: "Bad Campus", Address: "x", CampusID: 999})
	if !errors.Is(err, roomservice.ErrRoomCampusNotFound) {
		t.Fatalf("expected ErrRoomCampusNotFound, got %v", err)
	}
}

func TestRoomService_Create_InvalidType(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	_, err := svc.Create(roomservice.CreateRoomInput{
		Name:     "Bad Type",
		Address:  "x",
		CampusID: testCampusID(t, db),
		Type:     "invalid",
	})
	if !errors.Is(err, roomservice.ErrRoomInvalidType) {
		t.Fatalf("expected ErrRoomInvalidType, got %v", err)
	}
}

func TestRoomService_Create_ExplicitType(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := svc.Create(roomservice.CreateRoomInput{
		Name:     "Dynamic Room",
		Address:  "x",
		CampusID: testCampusID(t, db),
		Type:     roommodel.RoomTypeDynamic,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if r.Type != roommodel.RoomTypeDynamic {
		t.Errorf("type = %q, want dynamic", r.Type)
	}
}

func TestRoomService_Create_ExplicitDisabled(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{
		Name:    "Quiet Room",
		Address: "Building B",
		Enabled: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if r.Enabled {
		t.Errorf("expected Enabled false, got true")
	}
}

func TestRoomService_Create_DuplicateName(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	if _, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "A", Address: "x"}); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	_, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "A", Address: "y"})
	if !errors.Is(err, roomservice.ErrRoomNameTaken) {
		t.Fatalf("expected ErrRoomNameTaken, got %v", err)
	}
}

func TestRoomService_GetByID_NotFound(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	_, err := svc.GetByID(999)
	if !errors.Is(err, roomservice.ErrRoomNotFound) {
		t.Fatalf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestRoomService_List_FilterAndPaging(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	seed := []roomservice.CreateRoomInput{
		{Name: "Alpha", Address: "a1", Enabled: boolPtr(true)},
		{Name: "Beta", Address: "b1", Enabled: boolPtr(true)},
		{Name: "Gamma", Address: "g1", Enabled: boolPtr(false)},
		{Name: "Alpine", Address: "a2", Enabled: boolPtr(true)},
		{Name: "Delta", Address: "d1", Enabled: boolPtr(true)},
	}
	for _, in := range seed {
		if _, err := createRoom(t, db, svc, in); err != nil {
			t.Fatalf("seed create failed: %v", err)
		}
	}

	t.Run("all", func(t *testing.T) {
		items, total, err := svc.List(roomservice.ListRoomFilter{})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 5 || len(items) != 5 {
			t.Fatalf("expected 5 items, got %d (total %d)", len(items), total)
		}
		// Ordered by id ASC
		if items[0].Name != "Alpha" {
			t.Errorf("unexpected first item: %s", items[0].Name)
		}
	})

	t.Run("name_substring", func(t *testing.T) {
		items, total, err := svc.List(roomservice.ListRoomFilter{Name: "Al"})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		// "Alpha" and "Alpine" both contain "Al"
		if total != 2 || len(items) != 2 {
			t.Fatalf("expected 2 'Al' items, got %d (total %d)", len(items), total)
		}
		for _, r := range items {
			if r.Name != "Alpha" && r.Name != "Alpine" {
				t.Errorf("unexpected item: %s", r.Name)
			}
		}
	})

	t.Run("enabled_only", func(t *testing.T) {
		items, total, err := svc.List(roomservice.ListRoomFilter{EnabledOnly: true})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 4 {
			t.Fatalf("expected 4 enabled rooms, got %d", total)
		}
		for _, r := range items {
			if !r.Enabled {
				t.Errorf("disabled room returned in enabled-only list: %s", r.Name)
			}
		}
	})

	t.Run("pagination", func(t *testing.T) {
		items, total, err := svc.List(roomservice.ListRoomFilter{Page: 2, PageSize: 2})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 5 || len(items) != 2 {
			t.Fatalf("expected total 5 / page size 2, got total %d / %d items", total, len(items))
		}
		// page 2 = items at index 2,3 by id ASC
		if items[0].Name != "Gamma" || items[1].Name != "Alpine" {
			t.Errorf("unexpected page 2: %s, %s", items[0].Name, items[1].Name)
		}
	})
}

func TestRoomService_Update_Partial(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "Old", Address: "Old Addr"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	updated, err := svc.Update(r.ID, roomservice.UpdateRoomInput{Address: strPtr("New Addr")})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Address != "New Addr" {
		t.Errorf("expected address updated, got %q", updated.Address)
	}
	if updated.Name != "Old" {
		t.Errorf("expected name unchanged, got %q", updated.Name)
	}
	if !updated.Enabled {
		t.Errorf("expected enabled unchanged, got false")
	}
}

func TestRoomService_Update_CampusAndType(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	oldCampus := testCampusID(t, db)
	newCampus := roommodel.ClinicCampus{Name: "New Campus"}
	if err := db.Create(&newCampus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}

	r, err := svc.Create(roomservice.CreateRoomInput{
		Name:     "Room",
		Address:  "addr",
		CampusID: oldCampus,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	roomType := roommodel.RoomTypeDynamic
	updated, err := svc.Update(r.ID, roomservice.UpdateRoomInput{
		CampusID: &newCampus.ID,
		Type:     &roomType,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.CampusID == nil || *updated.CampusID != newCampus.ID {
		t.Errorf("campus_id = %v, want %d", updated.CampusID, newCampus.ID)
	}
	if updated.Campus == nil || updated.Campus.ID != newCampus.ID {
		t.Errorf("campus = %+v, want campus %d", updated.Campus, newCampus.ID)
	}
	if updated.Type != roommodel.RoomTypeDynamic {
		t.Errorf("type = %q, want dynamic", updated.Type)
	}
}

func TestRoomService_Update_InvalidCampus(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "Room", Address: "addr"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	badCampus := uint(999)
	_, err = svc.Update(r.ID, roomservice.UpdateRoomInput{CampusID: &badCampus})
	if !errors.Is(err, roomservice.ErrRoomCampusNotFound) {
		t.Fatalf("expected ErrRoomCampusNotFound, got %v", err)
	}
}

func TestRoomService_Update_InvalidType(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "Room", Address: "addr"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	badType := roommodel.RoomType("invalid")
	_, err = svc.Update(r.ID, roomservice.UpdateRoomInput{Type: &badType})
	if !errors.Is(err, roomservice.ErrRoomInvalidType) {
		t.Fatalf("expected ErrRoomInvalidType, got %v", err)
	}
}

func TestRoomService_Update_ToggleEnabled(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	updated, err := svc.Update(r.ID, roomservice.UpdateRoomInput{Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Enabled {
		t.Errorf("expected enabled toggled to false")
	}
}

func TestRoomService_Update_Disable_UnfinishedRecordRejected(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := db.Create(&recordmodel.ClinicRecord{RoomID: r.ID, Status: recordmodel.RecordStatusPending}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	_, err = svc.Update(r.ID, roomservice.UpdateRoomInput{Enabled: boolPtr(false)})
	if !errors.Is(err, roomservice.ErrRoomHasUnfinishedRecords) {
		t.Fatalf("expected ErrRoomHasUnfinishedRecords, got %v", err)
	}
}

func TestRoomService_Update_Disable_FinishedRecordAllowed(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := db.Create(&recordmodel.ClinicRecord{RoomID: r.ID, Status: recordmodel.RecordStatusCompleted}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	updated, err := svc.Update(r.ID, roomservice.UpdateRoomInput{Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Enabled {
		t.Errorf("expected room disabled, got enabled")
	}
}

func TestRoomService_Update_Enable_UnfinishedRecordAllowed(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "X", Address: "y", Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := db.Create(&recordmodel.ClinicRecord{RoomID: r.ID, Status: recordmodel.RecordStatusInProgress}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	updated, err := svc.Update(r.ID, roomservice.UpdateRoomInput{Enabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !updated.Enabled {
		t.Errorf("expected room enabled, got disabled")
	}
}

func TestRoomService_Update_DuplicateName(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	if _, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "A", Address: "x"}); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	r2, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "B", Address: "y"})
	if err != nil {
		t.Fatalf("second create failed: %v", err)
	}
	_, err = svc.Update(r2.ID, roomservice.UpdateRoomInput{Name: strPtr("A")})
	if !errors.Is(err, roomservice.ErrRoomNameTaken) {
		t.Fatalf("expected ErrRoomNameTaken, got %v", err)
	}
}

func TestRoomService_Update_NotFound(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	_, err := svc.Update(999, roomservice.UpdateRoomInput{Name: strPtr("x")})
	if !errors.Is(err, roomservice.ErrRoomNotFound) {
		t.Fatalf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestRoomService_Delete_Success(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	r, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := svc.Delete(r.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := svc.GetByID(r.ID); !errors.Is(err, roomservice.ErrRoomNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestRoomService_Delete_NotFound(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	if err := svc.Delete(999); !errors.Is(err, roomservice.ErrRoomNotFound) {
		t.Fatalf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestRoomService_Delete_InUse_ServiceDate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &roommodel.ClinicRoom{}, &schedulingmodel.ClinicServiceDate{}, &recordmodel.ClinicRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := roomservice.NewRoomService(db)

	room, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "R", Address: "a"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	roomID := room.ID
	if err := db.Create(&schedulingmodel.ClinicServiceDate{RoomID: &roomID, Title: "x"}).Error; err != nil {
		t.Fatalf("seed service date: %v", err)
	}

	if err := svc.Delete(room.ID); !errors.Is(err, roomservice.ErrRoomInUse) {
		t.Fatalf("expected ErrRoomInUse, got %v", err)
	}
}

func TestRoomService_Delete_InUse_Record(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &roommodel.ClinicRoom{}, &schedulingmodel.ClinicServiceDate{}, &recordmodel.ClinicRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := roomservice.NewRoomService(db)

	room, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "R", Address: "a"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	// A finished record still references the room, so deletion stays blocked
	// by the broader in-use check.
	if err := db.Create(&recordmodel.ClinicRecord{RoomID: room.ID, Status: recordmodel.RecordStatusCompleted}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	if err := svc.Delete(room.ID); !errors.Is(err, roomservice.ErrRoomInUse) {
		t.Fatalf("expected ErrRoomInUse, got %v", err)
	}
}

func TestRoomService_List_CampusAndTypeFilters(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	mainCampus := roommodel.ClinicCampus{Name: "Main"}
	if err := db.Create(&mainCampus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	otherCampus := roommodel.ClinicCampus{Name: "Other"}
	if err := db.Create(&otherCampus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}

	rooms := []roommodel.ClinicRoom{
		{Name: "Main Normal", Address: "a", Enabled: true, CampusID: &mainCampus.ID, Type: roommodel.RoomTypeNormal},
		{Name: "Main Dynamic", Address: "b", Enabled: true, CampusID: &mainCampus.ID, Type: roommodel.RoomTypeDynamic},
		{Name: "Other Normal", Address: "c", Enabled: true, CampusID: &otherCampus.ID, Type: roommodel.RoomTypeNormal},
	}
	for i := range rooms {
		if err := db.Create(&rooms[i]).Error; err != nil {
			t.Fatalf("seed room: %v", err)
		}
	}

	items, total, err := svc.List(roomservice.ListRoomFilter{CampusID: &mainCampus.ID})
	if err != nil {
		t.Fatalf("list by campus: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("campus filter: expected 2 rooms, got %d (total %d)", len(items), total)
	}
	for _, item := range items {
		if item.Campus == nil || item.Campus.ID != mainCampus.ID {
			t.Errorf("room %q campus = %+v, want campus %d", item.Name, item.Campus, mainCampus.ID)
		}
	}

	roomType := roommodel.RoomTypeNormal
	items, total, err = svc.List(roomservice.ListRoomFilter{Type: &roomType})
	if err != nil {
		t.Fatalf("list by type: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("type filter: expected 2 rooms, got %d (total %d)", len(items), total)
	}
	for _, item := range items {
		if item.Type != roommodel.RoomTypeNormal {
			t.Errorf("room %q type = %q, want normal", item.Name, item.Type)
		}
	}
}

func TestRoomService_Delete_UnfinishedRecord(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewRoomService(db)

	room, err := createRoom(t, db, svc, roomservice.CreateRoomInput{Name: "R", Address: "a"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if err := db.Create(&recordmodel.ClinicRecord{RoomID: room.ID, Status: recordmodel.RecordStatusPending}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	if err := svc.Delete(room.ID); !errors.Is(err, roomservice.ErrRoomHasUnfinishedRecords) {
		t.Fatalf("expected ErrRoomHasUnfinishedRecords, got %v", err)
	}
}
