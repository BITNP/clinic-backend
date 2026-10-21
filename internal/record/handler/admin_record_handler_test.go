package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	staffmodel "clinic-backend/internal/staff/model"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminRecordHandlerListSearchByRealname(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&roommodel.ClinicRoom{},
		&recordmodel.ClinicRecord{},
		&recordmodel.ClinicRecordDevice{},
		&recordmodel.ClinicRecordWorker{},
		&recordmodel.ClinicRecordArrival{},
		&recordmodel.ClinicRecordRejection{},
		&recordmodel.ClinicRecordReferral{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	room := roommodel.ClinicRoom{Name: "Room", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	appt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, rec := range []recordmodel.ClinicRecord{
		{ID: 1, Realname: "张三"},
		{ID: 2, Realname: "李四"},
		{ID: 3, Realname: "张三丰"},
	} {
		rec.User = "u"
		rec.PhoneNum = "p"
		rec.Status = recordmodel.RecordStatusPending
		rec.AppointmentTime = appt
		rec.QuestionDesc = "x"
		rec.RoomID = room.ID
		if err := db.Create(&rec).Error; err != nil {
			t.Fatalf("seed record %d: %v", rec.ID, err)
		}
	}

	h := NewAdminRecordHandler(recordservice.NewAdminRecordService(db))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/records?realname=张&page=1&pageSize=1", nil)
	c.Set("staff", staffmodel.ClinicStaff{ID: 1})

	h.List(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []struct {
			ID       uint   `json:"id"`
			Realname string `json:"realname"`
		} `json:"items"`
		Total    int64 `json:"total"`
		Page     int   `json:"page"`
		PageSize int   `json:"pageSize"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Total != 2 {
		t.Errorf("total = %d, want 2", resp.Total)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	if resp.Items[0].ID != 3 || resp.Items[0].Realname != "张三丰" {
		t.Errorf("item = %d/%q, want 3/张三丰", resp.Items[0].ID, resp.Items[0].Realname)
	}
	if resp.Page != 1 || resp.PageSize != 1 {
		t.Errorf("pagination = %d/%d, want 1/1", resp.Page, resp.PageSize)
	}
}
