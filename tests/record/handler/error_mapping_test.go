package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	recordhandler "clinic-backend/internal/record/handler"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	staffmodel "clinic-backend/internal/staff/model"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type staticActionStore struct {
	entries []recordservice.RecordActionEntry
}

func (s *staticActionStore) Push(context.Context, int, recordservice.RecordActionEntry, time.Duration) error {
	return nil
}

func (s *staticActionStore) Window(context.Context, int, time.Time) ([]recordservice.RecordActionEntry, error) {
	return append([]recordservice.RecordActionEntry(nil), s.entries...), nil
}

func (s *staticActionStore) Remove(context.Context, int, recordservice.RecordActionEntry) error {
	return nil
}

func TestAdminRecordHandlerRevertErrorResponses(t *testing.T) {
	tests := []struct {
		name       string
		seedRecord bool
		entry      recordservice.RecordActionEntry
		wantStatus int
	}{
		{
			name: "window expired",
			entry: recordservice.RecordActionEntry{
				RecordID:  1,
				ActorID:   7,
				ActionSeq: 1,
				CreatedAt: time.Now().UTC().Add(-10 * time.Minute),
			},
			wantStatus: http.StatusConflict,
		},
		{
			name: "record not found",
			entry: recordservice.RecordActionEntry{
				RecordID:  1,
				ActorID:   7,
				ActionSeq: 1,
				CreatedAt: time.Now().UTC(),
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "action superseded",
			seedRecord: true,
			entry: recordservice.RecordActionEntry{
				RecordID:  1,
				ActorID:   7,
				ActionSeq: 1,
				CreatedAt: time.Now().UTC(),
			},
			wantStatus: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupHandlerErrorDB(t)
			if tt.seedRecord {
				seedHandlerErrorRecord(t, db, recordmodel.RecordStatusConfirmed)
			}
			svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
			svc.SetRecordActionService(recordservice.NewRecordActionService(&staticActionStore{
				entries: []recordservice.RecordActionEntry{tt.entry},
			}, 5*time.Minute))

			w := callAdminRecordHandler(t, recordhandler.NewAdminRecordHandler(svc), http.MethodPost, "/api/admin/records/1/revert", nil)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestAdminRecordHandlerInvalidTransitionReturnsBadRequest(t *testing.T) {
	db := setupHandlerErrorDB(t)
	seedHandlerErrorRecord(t, db, recordmodel.RecordStatusConfirmed)
	handler := recordhandler.NewAdminRecordHandler(recordservice.NewAdminRecordService(testutil.RawDB(t, db)))

	w := callAdminRecordHandler(t, handler, http.MethodPost, "/api/admin/records/1/confirm", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func setupHandlerErrorDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fake database: %v", err)
	}
	if err := db.AutoMigrate(
		&roommodel.ClinicCampus{},
		&roommodel.ClinicRoom{},
		&recordmodel.ClinicRecord{},
		&recordmodel.ClinicRecordDevice{},
		&recordmodel.ClinicRecordWorker{},
		&recordmodel.ClinicRecordPhoto{},
		&recordmodel.ClinicRecordArrival{},
		&recordmodel.ClinicRecordRejection{},
		&recordmodel.ClinicRecordReferral{},
		&staffmodel.ClinicStaff{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedHandlerErrorRecord(t *testing.T, db *gorm.DB, status recordmodel.RecordStatus) recordmodel.ClinicRecord {
	t.Helper()
	campus := roommodel.ClinicCampus{Name: "Campus"}
	if err := db.Create(&campus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	room := roommodel.ClinicRoom{Name: "Room", Enabled: true, CampusID: &campus.ID}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	rec := recordmodel.ClinicRecord{
		ID:              1,
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Status:          status,
		AppointmentTime: time.Now().UTC().AddDate(0, 0, 1),
		QuestionDesc:    "x",
		RoomID:          room.ID,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}
	return rec
}

func callAdminRecordHandler(t *testing.T, handler *recordhandler.AdminRecordHandler, method, path string, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("staff", staffmodel.ClinicStaff{ID: 7})
	})
	router.Handle(method, "/api/admin/records/:id/revert", handler.Revert)
	router.Handle(method, "/api/admin/records/:id/confirm", handler.Confirm)

	if request == nil {
		request = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	return w
}
