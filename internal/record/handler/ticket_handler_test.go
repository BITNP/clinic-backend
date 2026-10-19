package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authhandler "clinic-backend/internal/auth/handler"
	recordhandler "clinic-backend/internal/record/handler"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTicketHandlerRouter(t *testing.T) (*gin.Engine, *gorm.DB, *recordservice.TicketService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&roommodel.ClinicRoom{},
		&schedulingmodel.ClinicServiceDate{},
		&recordmodel.ClinicRecord{},
		&recordmodel.ClinicRecordDevice{},
		&recordmodel.ClinicRecordArrival{},
		&recordmodel.ClinicRecordWorker{},
		&recordmodel.ClinicRecordRejection{},
		&recordmodel.ClinicRecordReferral{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := recordservice.NewTicketService(db, nil)
	h := recordhandler.NewTicketHandler(svc)

	r := gin.New()
	authed := r.Group("/api/tickets", authhandler.ClientAuthMiddleware(testutil.TicketAPIKey, 5*time.Minute))
	{
		authed.GET("", h.List)
		authed.POST("", h.Create)
		authed.GET("/working", h.Working)
		authed.GET("/finished", h.Finished)
		authed.GET("/:id", h.Get)
		authed.PUT("/:id", h.Update)
		authed.DELETE("/:id", h.Delete)
	}
	return r, db, svc
}

func TestTicketHandler_Create_HappyPath(t *testing.T) {
	r, db, _ := setupTicketHandlerRouter(t)
	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)

	body := map[string]any{
		"realname":         "张三",
		"phone_num":        "13800138000",
		"campus":           "中关村",
		"appointment_time": testutil.FutureTruncatedDate(7).Format("2006-01-02"),
		"description":      "笔电不开机",
		"model":            "ThinkPad X1",
		"password":         "",
		"user":             "1120221234",
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, testutil.SignedTicketReq(http.MethodPost, "/api/tickets", body, ""))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"url", "user", "status", "realname", "phone_num", "campus", "appointment_time", "description", "worker_description", "model", "reject_reason", "password"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing key %q in response: %+v", key, resp)
		}
	}
	if resp["campus"] != "中关村" {
		t.Errorf("campus: got %v", resp["campus"])
	}
	if resp["user"] != "1120221234" {
		t.Errorf("user should be from signed ?username=, got %v", resp["user"])
	}
}

func TestTicketHandler_Create_ValidationMsg(t *testing.T) {
	r, db, _ := setupTicketHandlerRouter(t)
	// Seed the room but no service date on the requested day — Step 1 fails.
	room := roommodel.ClinicRoom{Name: "中关村", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	body := map[string]any{
		"realname":         "张三",
		"phone_num":        "13800138000",
		"campus":           "中关村",
		"appointment_time": testutil.FutureTruncatedDate(7).Format("2006-01-02"),
		"description":      "x",
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, testutil.SignedTicketReq(http.MethodPost, "/api/tickets", body, ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["msg"] != "该日期诊所停止营业" {
		t.Errorf("expected Chinese msg, got %v", resp)
	}
}

func TestTicketHandler_Working_Empty(t *testing.T) {
	r, _, _ := setupTicketHandlerRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, testutil.SignedTicketReq(http.MethodGet, "/api/tickets/working", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["count"] != float64(0) {
		t.Errorf("expected count 0, got %v", resp["count"])
	}
	if _, exists := resp["data"]; exists {
		t.Errorf("expected no data key when count=0, got %+v", resp)
	}
}

func TestTicketHandler_Working_Found(t *testing.T) {
	r, db, svc := setupTicketHandlerRouter(t)
	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	if _, err := svc.Create(recordservice.CreateTicketInput{
		User: "1120221234", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, testutil.SignedTicketReq(http.MethodGet, "/api/tickets/working", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["count"] != float64(1) {
		t.Errorf("count: %v", resp["count"])
	}
	if _, ok := resp["data"]; !ok {
		t.Errorf("missing data key when count=1")
	}
}

func TestTicketHandler_Get_Forbidden(t *testing.T) {
	r, db, _ := setupTicketHandlerRouter(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	rec := recordmodel.ClinicRecord{User: "alice", Realname: "r", PhoneNum: "p", Status: recordmodel.RecordStatusPending, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, testutil.SignedTicketReq(http.MethodGet, "/api/tickets/"+testutil.Itoa(rec.ID), nil, "bob"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTicketHandler_Get_NotFound(t *testing.T) {
	r, _, _ := setupTicketHandlerRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, testutil.SignedTicketReq(http.MethodGet, "/api/tickets/999", nil, ""))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTicketHandler_Delete_NoAuth(t *testing.T) {
	r, _, _ := setupTicketHandlerRouter(t)
	// No auth headers at all.
	req := httptest.NewRequest(http.MethodDelete, "/api/tickets/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestTicketHandler_Delete_OK(t *testing.T) {
	r, db, svc := setupTicketHandlerRouter(t)
	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	rec, err := svc.Create(recordservice.CreateTicketInput{
		User: "1120221234", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, testutil.SignedTicketReq(http.MethodDelete, "/api/tickets/"+testutil.Itoa(rec.ID), nil, ""))
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTicketHandler_List_FinishedEnvelop(t *testing.T) {
	r, db, _ := setupTicketHandlerRouter(t)
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	finished := recordmodel.ClinicRecord{User: "1120221234", Realname: "r", PhoneNum: "p", Status: recordmodel.RecordStatusCompleted, AppointmentTime: testutil.FutureTruncatedDate(7), QuestionDesc: "x", RoomID: roomID}
	if err := db.Create(&finished).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, testutil.SignedTicketReq(http.MethodGet, "/api/tickets/finished", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items    []map[string]any `json:"items"`
		Total    int64            `json:"total"`
		Page     int              `json:"page"`
		PageSize int              `json:"pageSize"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Errorf("expected 1 item, got total %d items %d", resp.Total, len(resp.Items))
	}
}
