package announcement_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"clinic-backend/internal/announcement"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAnnouncementHandlerRouter(t *testing.T) (*gin.Engine, *announcement.AnnouncementService) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&announcement.ClinicAnnouncement{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	svc := announcement.NewAnnouncementService(db)
	h := announcement.NewAnnouncementHandler(svc)

	r := gin.New()
	g := r.Group("/api/admin/announcements")
	{
		g.POST("", h.Create)
		g.GET("", h.List)
		g.GET("/:id", h.Get)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
	return r, svc
}

func TestAnnouncementHandler_Create_Success(t *testing.T) {
	r, _ := setupAnnouncementHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/announcements", map[string]any{
		"title":      "Hello",
		"content":    "body text",
		"tag":        "normal",
		"brief":      "short",
		"expireDate": testutil.FutureDate(7).Format(time.RFC3339),
		"priority":   1,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var got announcement.ClinicAnnouncement
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Title != "Hello" || got.ID == 0 {
		t.Errorf("unexpected announcement: %+v", got)
	}
}

func TestAnnouncementHandler_Create_DateOnlyFormat(t *testing.T) {
	r, _ := setupAnnouncementHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/announcements", map[string]any{
		"title":      "Hello",
		"content":    "body text",
		"tag":        "normal",
		"brief":      "short",
		"expireDate": testutil.FutureDate(7).Format("2006-01-02"),
		"priority":   1,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAnnouncementHandler_Create_Validation(t *testing.T) {
	r, _ := setupAnnouncementHandlerRouter(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing title", map[string]any{"content": "c", "brief": "b", "expireDate": testutil.FutureDate(1).Format(time.RFC3339)}},
		{"title too long", map[string]any{"title": "012345678901234567890", "content": "c", "brief": "b", "expireDate": testutil.FutureDate(1).Format(time.RFC3339)}},
		{"past expireDate", map[string]any{"title": "T", "content": "c", "brief": "b", "expireDate": "2000-01-01T00:00:00Z"}},
		{"invalid expireDate", map[string]any{"title": "T", "content": "c", "brief": "b", "expireDate": "not-a-date"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/announcements", c.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAnnouncementHandler_Get_NotFound(t *testing.T) {
	r, _ := setupAnnouncementHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/announcements/999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAnnouncementHandler_List(t *testing.T) {
	r, svc := setupAnnouncementHandlerRouter(t)
	for i := 0; i < 3; i++ {
		if _, err := svc.Create(announcement.CreateAnnouncementInput{
			Title: "T", Content: "c", Tag: "normal", Brief: "b", ExpireDate: testutil.FutureDate(1), Priority: uint(i),
		}); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/announcements?page=1&pageSize=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items    []announcement.ClinicAnnouncement `json:"items"`
		Total    int64                             `json:"total"`
		Page     int                               `json:"page"`
		PageSize int                               `json:"pageSize"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 3 || len(resp.Items) != 2 {
		t.Errorf("expected total 3 / 2 items, got total %d / %d items", resp.Total, len(resp.Items))
	}
}

func TestAnnouncementHandler_Update_Partial(t *testing.T) {
	r, svc := setupAnnouncementHandlerRouter(t)
	a, err := svc.Create(announcement.CreateAnnouncementInput{
		Title: "Old", Content: "c", Tag: "normal", Brief: "b", ExpireDate: testutil.FutureDate(2),
	})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPut, "/api/admin/announcements/"+testutil.Itoa(a.ID), map[string]any{"title": "New"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got announcement.ClinicAnnouncement
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Title != "New" || got.Content != "c" {
		t.Errorf("expected title New and content unchanged, got %+v", got)
	}
}

func TestAnnouncementHandler_Delete_Success(t *testing.T) {
	r, svc := setupAnnouncementHandlerRouter(t)
	a, err := svc.Create(announcement.CreateAnnouncementInput{
		Title: "X", Content: "c", Tag: "normal", Brief: "b", ExpireDate: testutil.FutureDate(1),
	})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/announcements/"+testutil.Itoa(a.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAnnouncementHandler_InvalidID(t *testing.T) {
	r, _ := setupAnnouncementHandlerRouter(t)
	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/announcements/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
