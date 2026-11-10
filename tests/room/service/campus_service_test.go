package service_test

import (
	"errors"
	"testing"

	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"
)

func TestCampusService_Create(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)

	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "Main Campus"})
	if err != nil {
		t.Fatalf("create campus: %v", err)
	}
	if campus.ID == 0 {
		t.Fatal("expected ID to be assigned")
	}
	if campus.Name != "Main Campus" {
		t.Errorf("name = %q, want %q", campus.Name, "Main Campus")
	}
}

func TestCampusService_Create_DuplicateName(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)

	if _, err := svc.Create(roomservice.CreateCampusInput{Name: "A"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.Create(roomservice.CreateCampusInput{Name: "A"}); !errors.Is(err, roomservice.ErrCampusNameTaken) {
		t.Fatalf("expected ErrCampusNameTaken, got %v", err)
	}
}

func TestCampusService_GetByID_NotFound(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)

	if _, err := svc.GetByID(999); !errors.Is(err, roomservice.ErrCampusNotFound) {
		t.Fatalf("expected ErrCampusNotFound, got %v", err)
	}
}

func TestCampusService_List_FilterAndPaging(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)

	for _, name := range []string{"Alpha", "Beta", "Alpine", "Gamma", "Delta"} {
		if _, err := svc.Create(roomservice.CreateCampusInput{Name: name}); err != nil {
			t.Fatalf("seed campus %s: %v", name, err)
		}
	}

	t.Run("all", func(t *testing.T) {
		items, total, err := svc.List(roomservice.ListCampusFilter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 5 || len(items) != 5 {
			t.Fatalf("expected 5 items, got %d (total %d)", len(items), total)
		}
		if items[0].Name != "Alpha" {
			t.Errorf("first item = %q, want Alpha", items[0].Name)
		}
	})

	t.Run("name_substring", func(t *testing.T) {
		items, total, err := svc.List(roomservice.ListCampusFilter{Name: "Al"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 2 || len(items) != 2 {
			t.Fatalf("expected 2 items, got %d (total %d)", len(items), total)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		items, total, err := svc.List(roomservice.ListCampusFilter{Page: 2, PageSize: 2})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 5 || len(items) != 2 {
			t.Fatalf("expected total 5 / 2 items, got total %d / %d items", total, len(items))
		}
		if items[0].Name != "Alpine" || items[1].Name != "Gamma" {
			t.Errorf("unexpected page 2: %q, %q", items[0].Name, items[1].Name)
		}
	})
}

func TestCampusService_Update_Partial(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)

	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "Old"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := svc.Update(campus.ID, roomservice.UpdateCampusInput{Name: strPtr("New")})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "New" {
		t.Errorf("name = %q, want New", updated.Name)
	}
}

func TestCampusService_Update_DuplicateName(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)

	if _, err := svc.Create(roomservice.CreateCampusInput{Name: "A"}); err != nil {
		t.Fatalf("create A: %v", err)
	}
	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "B"})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if _, err := svc.Update(campus.ID, roomservice.UpdateCampusInput{Name: strPtr("A")}); !errors.Is(err, roomservice.ErrCampusNameTaken) {
		t.Fatalf("expected ErrCampusNameTaken, got %v", err)
	}
}

func TestCampusService_Delete(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)

	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "Empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(campus.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetByID(campus.ID); !errors.Is(err, roomservice.ErrCampusNotFound) {
		t.Fatalf("expected campus to be deleted, got %v", err)
	}
}

func TestCampusService_Delete_InUse(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)

	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "In Use"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	room := roommodel.ClinicRoom{
		Name:     "Room A",
		Address:  "addr",
		Enabled:  true,
		CampusID: &campus.ID,
		Type:     roommodel.RoomTypeNormal,
	}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	if err := svc.Delete(campus.ID); !errors.Is(err, roomservice.ErrCampusInUse) {
		t.Fatalf("expected ErrCampusInUse, got %v", err)
	}
}
