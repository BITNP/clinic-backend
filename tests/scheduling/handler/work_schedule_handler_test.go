package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	authhandler "clinic-backend/internal/auth/handler"
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulinghandler "clinic-backend/internal/scheduling/handler"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
	staffmodel "clinic-backend/internal/staff/model"
	staffservice "clinic-backend/internal/staff/service"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupWorkScheduleHandlerRouter(t *testing.T) (*gin.Engine, *schedulingservice.WorkScheduleService, *gorm.DB) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	if err := db.AutoMigrate(
		&schedulingmodel.ClinicWorkSchedule{},
		&schedulingmodel.ClinicWorkScheduleWeekday{},
		&schedulingmodel.ClinicWorkScheduleStaff{},
		&roommodel.ClinicRoom{},
		&staffmodel.ClinicStaff{},
		&staffmodel.ClinicStaffWorkyear{},
		&recordmodel.ClinicRecordWorker{},
	); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	svc := schedulingservice.NewWorkScheduleService(db)
	h := schedulinghandler.NewWorkScheduleHandler(svc)

	r := gin.New()
	staffRead := r.Group("/api/admin/work-schedules")
	staffRead.Use(func(c *gin.Context) {
		c.Set("staff_role", authhandler.RoleStaff)
		c.Next()
	})
	{
		staffRead.GET("", h.List)
		staffRead.GET("/:id", h.Get)
	}

	adminWrite := r.Group("/api/admin/work-schedules")
	adminWrite.Use(func(c *gin.Context) {
		c.Set("staff_role", authhandler.RoleAdmin)
		c.Next()
	})
	{
		adminWrite.GET("/all", h.ListAll)
		adminWrite.GET("/service-availability", h.ServiceAvailability)
		adminWrite.POST("", h.Create)
		adminWrite.PUT("/:id", h.Update)
		adminWrite.DELETE("/:id", h.Delete)
		adminWrite.POST("/:id/staff", h.AddStaff)
		adminWrite.DELETE("/:id/staff", h.RemoveStaff)
		adminWrite.GET("/:id/staff", h.ListStaff)
		adminWrite.GET("/:id/valid-staff", h.ListValidStaff)
		adminWrite.PUT("/:id/weekdays", h.UpdateWeekday)
	}
	return r, svc, db
}

func TestWorkScheduleHandler_Create_Success(t *testing.T) {
	r, _, db := setupWorkScheduleHandlerRouter(t)
	roomID := testutil.SeedRoom(t, db, "Room A")

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/work-schedules", map[string]any{
		"name":       "Fall 2026",
		"start_date": "2026-09-01",
		"end_date":   "2026-12-31",
		"weekdays": []map[string]any{
			{"weekday": 1, "start_time": "09:00", "end_time": "12:00", "room_id": roomID, "staff_ids": []int{}},
		},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var got schedulingmodel.ClinicWorkSchedule
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Name != "Fall 2026" || got.ID == 0 {
		t.Errorf("unexpected schedule: %+v", got)
	}
}

func TestWorkScheduleHandler_Create_Validation(t *testing.T) {
	r, _, _ := setupWorkScheduleHandlerRouter(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"start_date": "2026-01-01", "end_date": "2026-12-31"}},
		{"invalid start_date", map[string]any{"name": "X", "start_date": "bad", "end_date": "2026-12-31"}},
		{"invalid end_date", map[string]any{"name": "X", "start_date": "2026-01-01", "end_date": "bad"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/work-schedules", c.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestWorkScheduleHandler_List_OnlyEnabled(t *testing.T) {
	r, svc, _ := setupWorkScheduleHandlerRouter(t)

	_, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "Disabled", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Enabled: false,
	})
	if err != nil {
		t.Fatalf("seed disabled: %v", err)
	}
	_, err = svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "Enabled", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed enabled: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/work-schedules", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items    []schedulingmodel.ClinicWorkSchedule `json:"items"`
		Total    int64                                `json:"total"`
		Page     int                                  `json:"page"`
		PageSize int                                  `json:"pageSize"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Fatalf("expected 1 enabled item, got total %d / %d items", resp.Total, len(resp.Items))
	}
	if resp.Items[0].Name != "Enabled" {
		t.Errorf("expected Enabled, got %s", resp.Items[0].Name)
	}
}

func TestWorkScheduleHandler_Get_StaffSeesOnlyEnabled(t *testing.T) {
	r, svc, _ := setupWorkScheduleHandlerRouter(t)

	disabled, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "Disabled", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Enabled: false,
	})
	if err != nil {
		t.Fatalf("seed disabled: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/work-schedules/"+testutil.Itoa(disabled.ID), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for disabled schedule via staff endpoint, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkScheduleHandler_ListAll_Admin(t *testing.T) {
	r, svc, _ := setupWorkScheduleHandlerRouter(t)

	_, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "A", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Enabled: false,
	})
	if err != nil {
		t.Fatalf("seed A: %v", err)
	}
	_, err = svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "B", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/work-schedules/all", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items    []schedulingmodel.ClinicWorkSchedule `json:"items"`
		Total    int64                                `json:"total"`
		Page     int                                  `json:"page"`
		PageSize int                                  `json:"pageSize"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("expected 2 items, got %d", resp.Total)
	}
}

func TestWorkScheduleHandler_ServiceAvailability_Success(t *testing.T) {
	r, svc, db := setupWorkScheduleHandlerRouter(t)

	roomA := testutil.SeedRoom(t, db, "Room A")
	roomB := testutil.SeedRoom(t, db, "Room B")
	staffID := testutil.SeedOneStaff(t, db)
	testutil.SeedStaffWorkYear(t, db, staffID, 2026)

	_, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name:      "Enabled",
		StartDate: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		Enabled:   true,
		Weekdays: []schedulingservice.WeekdayInput{
			{Weekday: 1, StartTime: "09:00", EndTime: "12:00", RoomID: roomA, StaffIDs: []int{staffID}},
		},
	})
	if err != nil {
		t.Fatalf("create enabled schedule: %v", err)
	}

	from := "2026-07-20T00:00:00Z"
	to := "2026-07-26T00:00:00Z"
	roomIDs := fmt.Sprintf("%d,%d", roomA, roomB)
	path := fmt.Sprintf("/api/admin/work-schedules/service-availability?from=%s&to=%s&room_ids=%s", from, to, roomIDs)

	w := testutil.DoRequest(t, r, http.MethodGet, path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []schedulingservice.ServiceAvailabilityItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if len(resp.Items) != 14 {
		t.Fatalf("expected 14 items (7 days × 2 rooms), got %d", len(resp.Items))
	}

	for _, item := range resp.Items {
		switch {
		case item.RoomID == roomA && item.Date == "2026-07-20":
			if !item.Available {
				t.Errorf("expected room A on Monday 07-20 to be available")
			}
		case item.RoomID == roomA && item.Date == "2026-07-21":
			if item.Available {
				t.Errorf("expected room A on Tuesday to be unavailable")
			}
		case item.RoomID == roomB:
			if item.Available {
				t.Errorf("expected room B on any day to be unavailable")
			}
		case item.Date == "2026-07-25" || item.Date == "2026-07-26":
			if item.Available {
				t.Errorf("expected weekend to be unavailable (outside period)")
			}
		}
	}
}

func TestWorkScheduleHandler_ServiceAvailability_NoEnabled(t *testing.T) {
	r, _, _ := setupWorkScheduleHandlerRouter(t)

	from := "2026-07-20T00:00:00Z"
	to := "2026-07-20T00:00:00Z"
	path := fmt.Sprintf("/api/admin/work-schedules/service-availability?from=%s&to=%s&room_ids=1", from, to)

	w := testutil.DoRequest(t, r, http.MethodGet, path, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkScheduleHandler_ServiceAvailability_InvalidParams(t *testing.T) {
	r, _, _ := setupWorkScheduleHandlerRouter(t)

	tests := []struct {
		name string
		path string
	}{
		{"missing from", "/api/admin/work-schedules/service-availability?to=2026-07-20T00:00:00Z&room_ids=1"},
		{"missing to", "/api/admin/work-schedules/service-availability?from=2026-07-20T00:00:00Z&room_ids=1"},
		{"missing room_ids", "/api/admin/work-schedules/service-availability?from=2026-07-20T00:00:00Z&to=2026-07-26T00:00:00Z"},
		{"to before from", "/api/admin/work-schedules/service-availability?from=2026-07-26T00:00:00Z&to=2026-07-20T00:00:00Z&room_ids=1"},
		{"invalid room_id", "/api/admin/work-schedules/service-availability?from=2026-07-20T00:00:00Z&to=2026-07-26T00:00:00Z&room_ids=abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := testutil.DoRequest(t, r, http.MethodGet, tt.path, nil)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestWorkScheduleHandler_Update_Success(t *testing.T) {
	r, svc, _ := setupWorkScheduleHandlerRouter(t)

	created, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "Original", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/work-schedules/"+testutil.Itoa(created.ID),
		map[string]any{"name": "Updated"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkScheduleHandler_Delete_Success(t *testing.T) {
	r, svc, _ := setupWorkScheduleHandlerRouter(t)

	created, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "X", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/work-schedules/"+testutil.Itoa(created.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkScheduleHandler_AddRemoveStaff(t *testing.T) {
	r, svc, db := setupWorkScheduleHandlerRouter(t)
	roomID := testutil.SeedRoom(t, db, "R1")
	staffID := testutil.SeedOneStaff(t, db)

	testutil.SeedStaffWorkYear(t, db, staffID, 2026)

	created, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "Test", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Create a weekday directly in the DB so we have an ID for the test.
	var dbWD schedulingmodel.ClinicWorkScheduleWeekday
	if err := db.Create(&schedulingmodel.ClinicWorkScheduleWeekday{
		WorkScheduleID: created.ID,
		Weekday:        1,
		StartTime:      time.Date(1970, 1, 1, 9, 0, 0, 0, time.UTC),
		EndTime:        time.Date(1970, 1, 1, 12, 0, 0, 0, time.UTC),
		RoomID:         roomID,
	}).Error; err != nil {
		t.Fatalf("seed weekday: %v", err)
	}
	if err := db.Last(&dbWD).Error; err != nil {
		t.Fatalf("fetch weekday: %v", err)
	}

	// Add
	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/work-schedules/"+testutil.Itoa(created.ID)+"/staff",
		map[string]any{"weekday_id": dbWD.ID, "staff_id": staffID})
	if w.Code != http.StatusCreated {
		t.Fatalf("add staff: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var assign schedulingmodel.ClinicWorkScheduleStaff
	if err := json.Unmarshal(w.Body.Bytes(), &assign); err != nil {
		t.Fatalf("decode assign: %v", err)
	}
	if assign.ScheduleID != created.ID {
		t.Errorf("expected schedule_id %d, got %d", created.ID, assign.ScheduleID)
	}

	// Remove
	w = testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/work-schedules/"+testutil.Itoa(created.ID)+"/staff",
		map[string]any{"weekday_id": dbWD.ID, "staff_id": staffID})
	if w.Code != http.StatusNoContent {
		t.Fatalf("remove staff: expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkScheduleHandler_ListValidStaff(t *testing.T) {
	r, svc, db := setupWorkScheduleHandlerRouter(t)
	staffIDs := testutil.SeedStaff(t, db, 2)
	for _, sid := range staffIDs {
		testutil.SeedStaffWorkYear(t, db, sid, 2026)
	}

	created, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "Test", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/work-schedules/"+testutil.Itoa(created.ID)+"/valid-staff", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []staffservice.StaffListItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
}

func TestWorkScheduleHandler_UpdateWeekday_Update(t *testing.T) {
	r, svc, db := setupWorkScheduleHandlerRouter(t)
	roomID := testutil.SeedRoom(t, db, "Room A")

	created, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "Test", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		Weekdays: []schedulingservice.WeekdayInput{
			{Weekday: 1, StartTime: "09:00", EndTime: "12:00", RoomID: roomID, StaffIDs: []int{}},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/work-schedules/"+testutil.Itoa(created.ID)+"/weekdays", map[string]any{
		"room_id":    roomID,
		"weekday":    1,
		"start_time": "10:00",
		"end_time":   "13:00",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got schedulingmodel.ClinicWorkScheduleWeekday
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.StartTime.Format("15:04") != "10:00" || got.EndTime.Format("15:04") != "13:00" {
		t.Fatalf("unexpected times: start=%v end=%v", got.StartTime, got.EndTime)
	}
}

func TestWorkScheduleHandler_UpdateWeekday_Create(t *testing.T) {
	r, svc, db := setupWorkScheduleHandlerRouter(t)
	roomID := testutil.SeedRoom(t, db, "Room B")

	created, err := svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name: "Test", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/work-schedules/"+testutil.Itoa(created.ID)+"/weekdays", map[string]any{
		"room_id":    roomID,
		"weekday":    2,
		"start_time": "14:00",
		"end_time":   "17:00",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got schedulingmodel.ClinicWorkScheduleWeekday
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RoomID != roomID || got.Weekday != 2 {
		t.Fatalf("unexpected room/weekday: room=%d weekday=%d", got.RoomID, got.Weekday)
	}
	if got.StartTime.Format("15:04") != "14:00" || got.EndTime.Format("15:04") != "17:00" {
		t.Fatalf("unexpected times: start=%v end=%v", got.StartTime, got.EndTime)
	}
}
