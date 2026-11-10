package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	roomhandler "clinic-backend/internal/room/handler"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupCampusHandlerRouter(t *testing.T) (*gin.Engine, *roomservice.CampusService, *gorm.DB) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fake database: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &roommodel.ClinicRoom{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	campusSvc := roomservice.NewCampusService(db)
	roomSvc := roomservice.NewRoomService(db)
	h := roomhandler.NewCampusHandler(campusSvc, roomSvc)

	r := gin.New()
	g := r.Group("/api/admin/campuses")
	{
		g.POST("", h.Create)
		g.GET("", h.List)
		g.GET("/:id/rooms", h.ListRooms)
		g.GET("/:id", h.Get)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
	return r, campusSvc, db
}

func TestCampusHandler_Create_Success(t *testing.T) {
	r, _, _ := setupCampusHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/campuses", map[string]any{"name": "Main Campus"})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var got roommodel.ClinicCampus
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == 0 || got.Name != "Main Campus" {
		t.Errorf("unexpected campus: %+v", got)
	}
}

func TestCampusHandler_Create_Validation(t *testing.T) {
	r, _, _ := setupCampusHandlerRouter(t)

	for name, body := range map[string]map[string]any{
		"missing name":  {},
		"name too long": {"name": strings.Repeat("a", 65)},
	} {
		t.Run(name, func(t *testing.T) {
			w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/campuses", body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCampusHandler_Create_DuplicateName(t *testing.T) {
	r, _, _ := setupCampusHandlerRouter(t)

	if w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/campuses", map[string]any{"name": "A"}); w.Code != http.StatusCreated {
		t.Fatalf("seed create: %d %s", w.Code, w.Body.String())
	}
	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/campuses", map[string]any{"name": "A"})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCampusHandler_Get_NotFound(t *testing.T) {
	r, _, _ := setupCampusHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/campuses/999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCampusHandler_InvalidID(t *testing.T) {
	r, _, _ := setupCampusHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/campuses/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCampusHandler_List(t *testing.T) {
	r, svc, _ := setupCampusHandlerRouter(t)
	for _, name := range []string{"Alpha", "Alpine", "Beta"} {
		if _, err := svc.Create(roomservice.CreateCampusInput{Name: name}); err != nil {
			t.Fatalf("seed campus: %v", err)
		}
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/campuses?name=Al&page=1&pageSize=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Items []roommodel.ClinicCampus `json:"items"`
		Total int64                    `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 2 || len(got.Items) != 2 {
		t.Fatalf("expected 2 campuses, got %d (total %d)", len(got.Items), got.Total)
	}
}

func TestCampusHandler_Update_Partial(t *testing.T) {
	r, svc, _ := setupCampusHandlerRouter(t)
	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "Old"})
	if err != nil {
		t.Fatalf("seed campus: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/campuses/"+testutil.Itoa(campus.ID), map[string]any{"name": "New"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got roommodel.ClinicCampus
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "New" {
		t.Errorf("name = %q, want New", got.Name)
	}
}

func TestCampusHandler_Delete(t *testing.T) {
	r, svc, _ := setupCampusHandlerRouter(t)
	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "Empty"})
	if err != nil {
		t.Fatalf("seed campus: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/campuses/"+testutil.Itoa(campus.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCampusHandler_Delete_InUse(t *testing.T) {
	r, svc, db := setupCampusHandlerRouter(t)
	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "In Use"})
	if err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	if err := db.Create(&roommodel.ClinicRoom{
		Name:     "Room A",
		Address:  "addr",
		Enabled:  true,
		CampusID: &campus.ID,
		Type:     roommodel.RoomTypeNormal,
	}).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/campuses/"+testutil.Itoa(campus.ID), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCampusHandler_ListRooms(t *testing.T) {
	r, svc, db := setupCampusHandlerRouter(t)
	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "Main"})
	if err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	other, err := svc.Create(roomservice.CreateCampusInput{Name: "Other"})
	if err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	rooms := []roommodel.ClinicRoom{
		{Name: "Normal", Address: "a", Enabled: true, CampusID: &campus.ID, Type: roommodel.RoomTypeNormal},
		{Name: "Dynamic", Address: "b", Enabled: false, CampusID: &campus.ID, Type: roommodel.RoomTypeDynamic},
		{Name: "Other Room", Address: "c", Enabled: true, CampusID: &other.ID, Type: roommodel.RoomTypeNormal},
	}
	for i := range rooms {
		if err := db.Create(&rooms[i]).Error; err != nil {
			t.Fatalf("seed room: %v", err)
		}
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/campuses/"+testutil.Itoa(campus.ID)+"/rooms", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Items []roommodel.ClinicRoom `json:"items"`
		Total int64                  `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 2 || len(got.Items) != 2 {
		t.Fatalf("expected 2 rooms, got %d (total %d)", len(got.Items), got.Total)
	}
	for _, room := range got.Items {
		if room.Campus == nil || room.Campus.ID != campus.ID {
			t.Errorf("room %q campus = %+v, want campus %d", room.Name, room.Campus, campus.ID)
		}
	}

	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/campuses/"+testutil.Itoa(campus.ID)+"/rooms?type=dynamic", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Name != "Dynamic" {
		t.Fatalf("type filter returned %d rooms (total %d)", len(got.Items), got.Total)
	}

	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/campuses/"+testutil.Itoa(campus.ID)+"/rooms?enabled=true", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Name != "Normal" {
		t.Fatalf("enabled filter returned %d rooms (total %d)", len(got.Items), got.Total)
	}
}

func TestCampusHandler_ListRooms_NotFound(t *testing.T) {
	r, _, _ := setupCampusHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/campuses/999/rooms", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCampusHandler_ListRooms_InvalidType(t *testing.T) {
	r, svc, _ := setupCampusHandlerRouter(t)
	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "Main"})
	if err != nil {
		t.Fatalf("seed campus: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/campuses/"+testutil.Itoa(campus.ID)+"/rooms?type=unknown", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
