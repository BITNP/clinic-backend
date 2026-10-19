package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	recordmodel "clinic-backend/internal/record/model"
	roomhandler "clinic-backend/internal/room/handler"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRoomHandlerRouter(t *testing.T) (*gin.Engine, *roomservice.RoomService, *gorm.DB) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicRoom{}, &schedulingmodel.ClinicServiceDate{}, &recordmodel.ClinicRecord{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	svc := roomservice.NewRoomService(db)
	h := roomhandler.NewRoomHandler(svc)

	r := gin.New()
	g := r.Group("/api/admin/rooms")
	{
		g.POST("", h.Create)
		g.GET("", h.List)
		g.GET("/:id", h.Get)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
	return r, svc, db
}

func TestRoomHandler_Create_Success(t *testing.T) {
	r, _, _ := setupRoomHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/rooms", map[string]any{
		"name":    "Main Campus",
		"address": "123 University Ave",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var got roommodel.ClinicRoom
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Name != "Main Campus" || got.ID == 0 || !got.Enabled {
		t.Errorf("unexpected room: %+v", got)
	}
}

func TestRoomHandler_Create_Disabled(t *testing.T) {
	r, _, _ := setupRoomHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/rooms", map[string]any{
		"name":    "Closed Room",
		"address": "Somewhere",
		"enabled": false,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var got roommodel.ClinicRoom
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Enabled {
		t.Errorf("expected disabled, got %+v", got)
	}
}

func TestRoomHandler_Create_Validation(t *testing.T) {
	r, _, _ := setupRoomHandlerRouter(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"address": "a"}},
		{"missing address", map[string]any{"name": "n"}},
		{"name too long", map[string]any{"name": stringOfLen(65), "address": "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/rooms", c.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRoomHandler_Create_DuplicateName(t *testing.T) {
	r, _, _ := setupRoomHandlerRouter(t)

	if w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/rooms", map[string]any{"name": "A", "address": "x"}); w.Code != http.StatusCreated {
		t.Fatalf("first create failed: %d %s", w.Code, w.Body.String())
	}

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/rooms", map[string]any{"name": "A", "address": "y"})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoomHandler_Get_NotFound(t *testing.T) {
	r, _, _ := setupRoomHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/rooms/999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoomHandler_List(t *testing.T) {
	r, svc, _ := setupRoomHandlerRouter(t)
	for _, in := range []roomservice.CreateRoomInput{
		{Name: "Alpha", Address: "a"},
		{Name: "Beta", Address: "b", Enabled: boolPtr(false)},
		{Name: "Alpine", Address: "c"},
	} {
		if _, err := svc.Create(in); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/rooms?name=Al&page=1&pageSize=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items    []roommodel.ClinicRoom `json:"items"`
		Total    int64                  `json:"total"`
		Page     int                    `json:"page"`
		PageSize int                    `json:"pageSize"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("expected 2 'Al' items, got total %d / %d items", resp.Total, len(resp.Items))
	}
}

func TestRoomHandler_Update_Partial(t *testing.T) {
	r, svc, _ := setupRoomHandlerRouter(t)
	room, err := svc.Create(roomservice.CreateRoomInput{Name: "Old", Address: "Old"})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/rooms/"+testutil.Itoa(room.ID), map[string]any{"address": "New Addr"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got roommodel.ClinicRoom
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Address != "New Addr" || got.Name != "Old" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestRoomHandler_Update_DuplicateName(t *testing.T) {
	r, svc, _ := setupRoomHandlerRouter(t)
	if _, err := svc.Create(roomservice.CreateRoomInput{Name: "A", Address: "x"}); err != nil {
		t.Fatalf("seed A failed: %v", err)
	}
	room2, err := svc.Create(roomservice.CreateRoomInput{Name: "B", Address: "y"})
	if err != nil {
		t.Fatalf("seed B failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/rooms/"+testutil.Itoa(room2.ID), map[string]any{"name": "A"})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoomHandler_Update_Disable_UnfinishedRecord(t *testing.T) {
	r, svc, db := setupRoomHandlerRouter(t)
	room, err := svc.Create(roomservice.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := db.Create(&recordmodel.ClinicRecord{RoomID: room.ID, Status: recordmodel.RecordStatusPending}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/rooms/"+testutil.Itoa(room.ID), map[string]any{"enabled": false})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoomHandler_Delete_UnfinishedRecord(t *testing.T) {
	r, svc, db := setupRoomHandlerRouter(t)
	room, err := svc.Create(roomservice.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := db.Create(&recordmodel.ClinicRecord{RoomID: room.ID, Status: recordmodel.RecordStatusInProgress}).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/rooms/"+testutil.Itoa(room.ID), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoomHandler_Delete_Success(t *testing.T) {
	r, svc, _ := setupRoomHandlerRouter(t)
	room, err := svc.Create(roomservice.CreateRoomInput{Name: "X", Address: "y"})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/rooms/"+testutil.Itoa(room.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoomHandler_InvalidID(t *testing.T) {
	r, _, _ := setupRoomHandlerRouter(t)
	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/rooms/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// stringOfLen returns a string of the given length.
func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func boolPtr(v bool) *bool { return &v }
