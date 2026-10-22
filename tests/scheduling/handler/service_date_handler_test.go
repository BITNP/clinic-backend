package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulinghandler "clinic-backend/internal/scheduling/handler"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupServiceDateHandlerRouter(t *testing.T) (*gin.Engine, *schedulingservice.ServiceDateService, *gorm.DB) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicRoom{}, &schedulingmodel.ClinicServiceDate{}, &recordmodel.ClinicRecord{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	testutil.MustCreateRoom(t, db, 1)
	testutil.MustCreateRoom(t, db, 2)
	svc := schedulingservice.NewServiceDateService(db, nil)
	h := schedulinghandler.NewServiceDateHandler(svc)

	r := gin.New()
	g := r.Group("/api/admin/service-dates")
	{
		g.POST("", h.Create)
		g.GET("", h.ListUpcoming)
		g.GET("/all", h.ListAll)
		g.GET("/:id", h.Get)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
	return r, svc, db
}

func validServiceDateBody(roomID uint, daysFromNow int, cap uint) map[string]any {
	d := testutil.FutureDate(daysFromNow)
	start := time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, time.UTC)
	end := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, time.UTC)
	return map[string]any{
		"capacity":  cap,
		"room_id":   roomID,
		"date":      d.Format(time.RFC3339),
		"startTime": start.Format(time.RFC3339),
		"endTime":   end.Format(time.RFC3339),
		"title":     "Open Clinic",
	}
}

func TestServiceDateHandler_Create_Success(t *testing.T) {
	r, _, _ := setupServiceDateHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/service-dates", validServiceDateBody(1, 5, 10))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var got schedulingmodel.ClinicServiceDate
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Title != "Open Clinic" || got.ID == 0 {
		t.Errorf("unexpected service date: %+v", got)
	}
}

func TestServiceDateHandler_Create_Validation(t *testing.T) {
	r, _, _ := setupServiceDateHandlerRouter(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			"past date",
			func() map[string]any {
				b := validServiceDateBody(1, -2, 5)
				return b
			}(),
		},
		{
			"endTime before startTime",
			func() map[string]any {
				b := validServiceDateBody(1, 1, 5)
				start := time.Date(testutil.FutureDate(1).Year(), testutil.FutureDate(1).Month(), testutil.FutureDate(1).Day(), 12, 0, 0, 0, time.UTC)
				end := time.Date(testutil.FutureDate(1).Year(), testutil.FutureDate(1).Month(), testutil.FutureDate(1).Day(), 9, 0, 0, 0, time.UTC)
				b["startTime"] = start.Format(time.RFC3339)
				b["endTime"] = end.Format(time.RFC3339)
				return b
			}(),
		},
		{
			"endTime crosses midnight",
			func() map[string]any {
				b := validServiceDateBody(1, 1, 5)
				b["endTime"] = testutil.FutureDate(2).Add(1 * time.Hour).Format(time.RFC3339)
				return b
			}(),
		},
		{
			"missing capacity",
			func() map[string]any {
				b := validServiceDateBody(1, 1, 5)
				delete(b, "capacity")
				return b
			}(),
		},
		{
			"title too long",
			func() map[string]any {
				b := validServiceDateBody(1, 1, 5)
				b["title"] = "012345678901234567890"
				return b
			}(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/service-dates", c.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestServiceDateHandler_Get_NotFound(t *testing.T) {
	r, _, _ := setupServiceDateHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/service-dates/999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceDateHandler_Create_RoomNotFound(t *testing.T) {
	r, _, _ := setupServiceDateHandlerRouter(t)

	body := validServiceDateBody(999, 5, 5)
	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/service-dates", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown room, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceDateHandler_Update_RoomNotFound(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 3, 10))

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/service-dates/"+testutil.Itoa(d.ID), map[string]any{"room_id": 999})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown room, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceDateHandler_List(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 2, 5))
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 4, 5))
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(2, 6, 5))

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/service-dates?page=1&pageSize=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items    []schedulingmodel.ClinicServiceDate `json:"items"`
		Total    int64                               `json:"total"`
		Page     int                                 `json:"page"`
		PageSize int                                 `json:"pageSize"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 3 || len(resp.Items) != 2 {
		t.Errorf("expected total 3 / 2 items, got total %d / %d items", resp.Total, len(resp.Items))
	}
}

func TestServiceDateHandler_List_DefaultExcludesPast(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, -2, 5))
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 2, 5))

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/service-dates", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []schedulingmodel.ClinicServiceDate `json:"items"`
		Total int64                               `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Errorf("expected 1 future item, got total %d / %d items", resp.Total, len(resp.Items))
	}

	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/service-dates/all", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Errorf("expected 2 items from /all, got total %d / %d items", resp.Total, len(resp.Items))
	}
}

func TestServiceDateHandler_Update_Partial(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 3, 10))

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/service-dates/"+testutil.Itoa(d.ID), map[string]any{"title": "New Title"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got schedulingmodel.ClinicServiceDate
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Title != "New Title" || got.Capacity != 10 {
		t.Errorf("expected title update and capacity unchanged, got %+v", got)
	}
}

func TestServiceDateHandler_Update_InUse(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 3, 10))
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
	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/service-dates/"+testutil.Itoa(d.ID), map[string]any{"title": "x"})
	if w.Code != http.StatusOK {
		t.Fatalf("title update should succeed, got %d: %s", w.Code, w.Body.String())
	}

	// A full payload with unchanged room/date should still allow capacity edits.
	w = testutil.DoRequest(t, r, http.MethodPut, "/api/admin/service-dates/"+testutil.Itoa(d.ID), map[string]any{
		"title":     "x",
		"room_id":   1,
		"capacity":  11,
		"date":      d.Date,
		"startTime": d.StartTime,
		"endTime":   d.EndTime,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("full unchanged payload should succeed, got %d: %s", w.Code, w.Body.String())
	}

	// Room changes should be blocked when records exist.
	w = testutil.DoRequest(t, r, http.MethodPut, "/api/admin/service-dates/"+testutil.Itoa(d.ID), map[string]any{"room_id": 2})
	if w.Code != http.StatusConflict {
		t.Fatalf("room change should be blocked, expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// Capacity changes below booked count should be blocked.
	w = testutil.DoRequest(t, r, http.MethodPut, "/api/admin/service-dates/"+testutil.Itoa(d.ID), map[string]any{"capacity": 0})
	if w.Code != http.StatusConflict {
		t.Fatalf("capacity below booked should be blocked, expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// Capacity changes at or above booked count should be allowed.
	w = testutil.DoRequest(t, r, http.MethodPut, "/api/admin/service-dates/"+testutil.Itoa(d.ID), map[string]any{"capacity": 10})
	if w.Code != http.StatusOK {
		t.Fatalf("capacity >= booked should succeed, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceDateHandler_Delete_Success(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 3, 10))

	w := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/service-dates/"+testutil.Itoa(d.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceDateHandler_Delete_InUse(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 3, 10))
	if err := db.Create(&recordmodel.ClinicRecord{
		Realname:        "x",
		PhoneNum:        "000",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: d.Date,
		RoomID:          1,
	}).Error; err != nil {
		t.Fatalf("seed record failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/service-dates/"+testutil.Itoa(d.ID), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceDateHandler_InvalidID(t *testing.T) {
	r, _, _ := setupServiceDateHandlerRouter(t)
	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/service-dates/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceDateHandler_List_ExplicitRangeIncludesPast(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, -2, 5)) // past
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 2, 5))
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(2, 6, 5))

	w := testutil.DoRequest(t, r, http.MethodGet,
		"/api/admin/service-dates?from=2020-01-01T00:00:00Z&to="+testutil.FutureDate(4).Format(time.RFC3339), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []schedulingmodel.ClinicServiceDate `json:"items"`
		Total int64                               `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Errorf("expected past + day2 items, got total %d / %d items", resp.Total, len(resp.Items))
	}
}

func TestServiceDateHandler_List_RoomIDs(t *testing.T) {
	r, svc, db := setupServiceDateHandlerRouter(t)
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, -2, 5)) // past, excluded by active-only
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 2, 5))
	testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(2, 6, 5))

	cases := []struct {
		name      string
		query     string
		wantTotal int
	}{
		{"room_ids multi", "room_ids=1,2", 2},
		{"room_ids single", "room_ids=2", 1},
		{"room_id single", "room_id=2", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/service-dates?"+c.query, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
			var resp struct {
				Items []schedulingmodel.ClinicServiceDate `json:"items"`
				Total int64                               `json:"total"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if int(resp.Total) != c.wantTotal || len(resp.Items) != c.wantTotal {
				t.Errorf("expected total %d / %d items, got total %d / %d items", c.wantTotal, c.wantTotal, resp.Total, len(resp.Items))
			}
		})
	}
}

func TestServiceDateHandler_List_InvalidFilters(t *testing.T) {
	r, _, _ := setupServiceDateHandlerRouter(t)

	later := testutil.FutureDate(10).Format(time.RFC3339)
	earlier := testutil.FutureDate(2).Format(time.RFC3339)

	cases := []struct {
		name  string
		query string
	}{
		{"invalid from", "from=not-a-date"},
		{"invalid to", "to=not-a-date"},
		{"to before from", "from=" + later + "&to=" + earlier},
		{"invalid room_ids element", "room_ids=1,abc"},
		{"invalid room_ids", "room_ids=abc"},
		{"invalid room_id", "room_id=abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/service-dates?"+c.query, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestServiceDateHandler_ClientList_AvailableOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicRoom{}, &schedulingmodel.ClinicServiceDate{}, &recordmodel.ClinicRecord{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	testutil.MustCreateRoom(t, db, 1)
	svc := schedulingservice.NewServiceDateService(db, nil)
	h := schedulinghandler.NewServiceDateHandler(svc)
	r := gin.New()
	r.GET("/api/service-dates", h.List)

	d := testutil.MustCreateServiceDate(t, db, svc, testutil.ValidServiceDateInput(1, 3, 1))
	if err := db.Create(&recordmodel.ClinicRecord{
		Realname:        "x",
		PhoneNum:        "000",
		Status:          recordmodel.RecordStatusConfirmed,
		AppointmentTime: d.Date,
		RoomID:          1,
	}).Error; err != nil {
		t.Fatalf("seed record failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/service-dates?active=true&available=true", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []schedulingmodel.ClinicServiceDate `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("expected 0 available items since full, got %d", len(resp.Items))
	}
}
