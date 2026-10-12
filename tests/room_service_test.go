package tests

import (
	"errors"
	"testing"

	"clinic-backend/models"
	"clinic-backend/services"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRoomServiceDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&models.ClinicRoom{}, &models.ClinicServiceDate{}, &models.ClinicRecord{}); err != nil {
		t.Fatalf("failed to migrate models: %v", err)
	}
	return db
}

func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }

func TestRoomService_Create_Defaults(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	r, err := svc.Create(services.CreateRoomInput{
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
}

func TestRoomService_Create_ExplicitDisabled(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	r, err := svc.Create(services.CreateRoomInput{
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
	svc := services.NewRoomService(db)

	if _, err := svc.Create(services.CreateRoomInput{Name: "A", Address: "x"}); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	_, err := svc.Create(services.CreateRoomInput{Name: "A", Address: "y"})
	if !errors.Is(err, services.ErrRoomNameTaken) {
		t.Fatalf("expected ErrRoomNameTaken, got %v", err)
	}
}

func TestRoomService_GetByID_NotFound(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	_, err := svc.GetByID(999)
	if !errors.Is(err, services.ErrRoomNotFound) {
		t.Fatalf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestRoomService_List_FilterAndPaging(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	seed := []services.CreateRoomInput{
		{Name: "Alpha", Address: "a1", Enabled: boolPtr(true)},
		{Name: "Beta", Address: "b1", Enabled: boolPtr(true)},
		{Name: "Gamma", Address: "g1", Enabled: boolPtr(false)},
		{Name: "Alpine", Address: "a2", Enabled: boolPtr(true)},
		{Name: "Delta", Address: "d1", Enabled: boolPtr(true)},
	}
	for _, in := range seed {
		if _, err := svc.Create(in); err != nil {
			t.Fatalf("seed create failed: %v", err)
		}
	}

	t.Run("all", func(t *testing.T) {
		items, total, err := svc.List(services.ListRoomFilter{})
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
		items, total, err := svc.List(services.ListRoomFilter{Name: "Al"})
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
		items, total, err := svc.List(services.ListRoomFilter{EnabledOnly: true})
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
		items, total, err := svc.List(services.ListRoomFilter{Page: 2, PageSize: 2})
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
	svc := services.NewRoomService(db)

	r, err := svc.Create(services.CreateRoomInput{Name: "Old", Address: "Old Addr"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	updated, err := svc.Update(r.ID, services.UpdateRoomInput{Address: strPtr("New Addr")})
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

func TestRoomService_Update_ToggleEnabled(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	r, err := svc.Create(services.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	updated, err := svc.Update(r.ID, services.UpdateRoomInput{Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Enabled {
		t.Errorf("expected enabled toggled to false")
	}
}

func TestRoomService_Update_Disable_UnfinishedRecordRejected(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	r, err := svc.Create(services.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := db.Create(&models.ClinicRecord{RoomID: r.ID, Status: models.RecordStatusPending}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	_, err = svc.Update(r.ID, services.UpdateRoomInput{Enabled: boolPtr(false)})
	if !errors.Is(err, services.ErrRoomHasUnfinishedRecords) {
		t.Fatalf("expected ErrRoomHasUnfinishedRecords, got %v", err)
	}
}

func TestRoomService_Update_Disable_FinishedRecordAllowed(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	r, err := svc.Create(services.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := db.Create(&models.ClinicRecord{RoomID: r.ID, Status: models.RecordStatusCompleted}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	updated, err := svc.Update(r.ID, services.UpdateRoomInput{Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Enabled {
		t.Errorf("expected room disabled, got enabled")
	}
}

func TestRoomService_Update_Enable_UnfinishedRecordAllowed(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	r, err := svc.Create(services.CreateRoomInput{Name: "X", Address: "y", Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := db.Create(&models.ClinicRecord{RoomID: r.ID, Status: models.RecordStatusInProgress}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	updated, err := svc.Update(r.ID, services.UpdateRoomInput{Enabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !updated.Enabled {
		t.Errorf("expected room enabled, got disabled")
	}
}

func TestRoomService_Update_DuplicateName(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	if _, err := svc.Create(services.CreateRoomInput{Name: "A", Address: "x"}); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	r2, err := svc.Create(services.CreateRoomInput{Name: "B", Address: "y"})
	if err != nil {
		t.Fatalf("second create failed: %v", err)
	}
	_, err = svc.Update(r2.ID, services.UpdateRoomInput{Name: strPtr("A")})
	if !errors.Is(err, services.ErrRoomNameTaken) {
		t.Fatalf("expected ErrRoomNameTaken, got %v", err)
	}
}

func TestRoomService_Update_NotFound(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	_, err := svc.Update(999, services.UpdateRoomInput{Name: strPtr("x")})
	if !errors.Is(err, services.ErrRoomNotFound) {
		t.Fatalf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestRoomService_Delete_Success(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	r, err := svc.Create(services.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := svc.Delete(r.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := svc.GetByID(r.ID); !errors.Is(err, services.ErrRoomNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestRoomService_Delete_NotFound(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	if err := svc.Delete(999); !errors.Is(err, services.ErrRoomNotFound) {
		t.Fatalf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestRoomService_Delete_InUse_ServiceDate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&models.ClinicRoom{}, &models.ClinicServiceDate{}, &models.ClinicRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := services.NewRoomService(db)

	room, err := svc.Create(services.CreateRoomInput{Name: "R", Address: "a"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	roomID := room.ID
	if err := db.Create(&models.ClinicServiceDate{RoomID: &roomID, Title: "x"}).Error; err != nil {
		t.Fatalf("seed service date: %v", err)
	}

	if err := svc.Delete(room.ID); !errors.Is(err, services.ErrRoomInUse) {
		t.Fatalf("expected ErrRoomInUse, got %v", err)
	}
}

func TestRoomService_Delete_InUse_Record(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&models.ClinicRoom{}, &models.ClinicServiceDate{}, &models.ClinicRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := services.NewRoomService(db)

	room, err := svc.Create(services.CreateRoomInput{Name: "R", Address: "a"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	// A finished record still references the room, so deletion stays blocked
	// by the broader in-use check.
	if err := db.Create(&models.ClinicRecord{RoomID: room.ID, Status: models.RecordStatusCompleted}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	if err := svc.Delete(room.ID); !errors.Is(err, services.ErrRoomInUse) {
		t.Fatalf("expected ErrRoomInUse, got %v", err)
	}
}

func TestRoomService_Delete_UnfinishedRecord(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := services.NewRoomService(db)

	room, err := svc.Create(services.CreateRoomInput{Name: "R", Address: "a"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if err := db.Create(&models.ClinicRecord{RoomID: room.ID, Status: models.RecordStatusPending}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	if err := svc.Delete(room.ID); !errors.Is(err, services.ErrRoomHasUnfinishedRecords) {
		t.Fatalf("expected ErrRoomHasUnfinishedRecords, got %v", err)
	}
}
