package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	recordhandler "clinic-backend/internal/record/handler"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupUserTicketBanHandlerRouter(t *testing.T) (*gin.Engine, *gorm.DB, *roommodel.ClinicCampus) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &recordmodel.ClinicUserTicketBan{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	campus := roommodel.ClinicCampus{Name: "Main"}
	if err := db.Create(&campus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}

	h := recordhandler.NewUserTicketBanHandler(recordservice.NewUserTicketBanService(testutil.RawDB(t, db)))
	r := gin.New()
	r.GET("/api/admin/user-ticket-bans", h.List)
	r.GET("/api/admin/user-ticket-bans/:id", h.Get)
	r.POST("/api/admin/user-ticket-bans", h.Create)
	r.PUT("/api/admin/user-ticket-bans/:id", h.Update)
	r.DELETE("/api/admin/user-ticket-bans/:id", h.Delete)
	return r, db, &campus
}

func TestUserTicketBanHandler_CRUD(t *testing.T) {
	r, _, campus := setupUserTicketBanHandlerRouter(t)
	future := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	create := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/user-ticket-bans", map[string]any{
		"user_id":     " 1120221234 ",
		"scope":       "campus",
		"campus_id":   campus.ID,
		"reason":      " repeated abuse ",
		"expirate_at": future.Format(time.RFC3339Nano),
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", create.Code, create.Body.String())
	}
	var created struct {
		ID         uint   `json:"id"`
		UserID     string `json:"user_id"`
		Scope      string `json:"scope"`
		CampusID   *uint  `json:"campus_id"`
		Reason     string `json:"reason"`
		ExpirateAt string `json:"expirate_at"`
		Active     bool   `json:"active"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == 0 ||
		created.UserID != "1120221234" ||
		created.Scope != "campus" ||
		created.CampusID == nil ||
		*created.CampusID != campus.ID ||
		created.Reason != "repeated abuse" ||
		!created.Active {
		t.Fatalf("created ban = %+v", created)
	}

	get := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/user-ticket-bans/"+testutil.Itoa(created.ID), nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d: %s", get.Code, get.Body.String())
	}

	list := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/user-ticket-bans?scope=campus&active=true", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", list.Code, list.Body.String())
	}
	var listed struct {
		Items []struct {
			ID uint `json:"id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("listed bans = %+v", listed)
	}

	newExpiry := future.Add(48 * time.Hour)
	update := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/user-ticket-bans/"+testutil.Itoa(created.ID), map[string]any{
		"reason":      "updated reason",
		"expirate_at": newExpiry.Format(time.RFC3339Nano),
	})
	if update.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", update.Code, update.Body.String())
	}
	var updated struct {
		Reason     string `json:"reason"`
		ExpirateAt string `json:"expirate_at"`
	}
	if err := json.Unmarshal(update.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	updatedExpiry, err := time.Parse(time.RFC3339Nano, updated.ExpirateAt)
	if err != nil {
		t.Fatalf("parse updated expiry: %v", err)
	}
	if updated.Reason != "updated reason" || !updatedExpiry.Equal(newExpiry) {
		t.Fatalf("updated ban = %+v", updated)
	}

	deleted := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/user-ticket-bans/"+testutil.Itoa(created.ID), nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	missing := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/user-ticket-bans/"+testutil.Itoa(created.ID), nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("get deleted: expected 404, got %d: %s", missing.Code, missing.Body.String())
	}
}

func TestUserTicketBanHandler_ValidationAndConflict(t *testing.T) {
	r, _, campus := setupUserTicketBanHandlerRouter(t)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)

	valid := map[string]any{
		"user_id":     "1120221234",
		"scope":       "campus",
		"campus_id":   campus.ID,
		"reason":      "reason",
		"expirate_at": future,
	}
	if got := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/user-ticket-bans", valid); got.Code != http.StatusCreated {
		t.Fatalf("seed ban: expected 201, got %d: %s", got.Code, got.Body.String())
	}
	if got := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/user-ticket-bans", valid); got.Code != http.StatusConflict {
		t.Fatalf("duplicate: expected 409, got %d: %s", got.Code, got.Body.String())
	}

	tests := []struct {
		name string
		path string
		body any
		want int
	}{
		{
			name: "malformed expiry",
			path: "/api/admin/user-ticket-bans",
			body: map[string]any{
				"user_id": "u", "scope": "global", "reason": "reason", "expirate_at": "tomorrow",
			},
			want: http.StatusBadRequest,
		},
		{
			name: "global with campus",
			path: "/api/admin/user-ticket-bans",
			body: map[string]any{
				"user_id": "u", "scope": "global", "campus_id": campus.ID, "reason": "reason", "expirate_at": future,
			},
			want: http.StatusBadRequest,
		},
		{
			name: "invalid list scope",
			path: "/api/admin/user-ticket-bans?scope=all",
			want: http.StatusBadRequest,
		},
		{
			name: "invalid list campus",
			path: "/api/admin/user-ticket-bans?campus_id=zero",
			want: http.StatusBadRequest,
		},
		{
			name: "invalid list active",
			path: "/api/admin/user-ticket-bans?active=yes",
			want: http.StatusBadRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodPost
			if tc.body == nil {
				method = http.MethodGet
			}
			got := testutil.DoRequest(t, r, method, tc.path, tc.body)
			if got.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, got.Code, got.Body.String())
			}
		})
	}
}
