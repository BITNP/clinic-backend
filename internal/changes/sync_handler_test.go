package changes_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	authhandler "clinic-backend/internal/auth/handler"
	"clinic-backend/internal/changes"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

// setupSyncHandlerRouter wires the sync handler behind a fake staff role and a
// write group carrying the bump middleware, plus one endpoint that fails.
func setupSyncHandlerRouter(t *testing.T) (*gin.Engine, *changes.SyncService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	svc := changes.NewSyncService(newFakeSyncStore())
	h := changes.NewSyncHandler(svc, 10*time.Second)

	r := gin.New()

	syncAdmin := r.Group("/api/admin/sync")
	syncAdmin.Use(func(c *gin.Context) {
		c.Set("staff_role", authhandler.RoleStaff)
		c.Next()
	})
	{
		syncAdmin.GET("", h.Get)
		syncAdmin.GET("/config", h.GetConfig)
	}

	records := r.Group("/api/admin/records")
	records.Use(changes.SyncBumpMiddleware(svc, changes.SyncGroupRecord))
	{
		records.GET("", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": []any{}}) })
		records.POST("", func(c *gin.Context) { c.JSON(http.StatusCreated, gin.H{"ok": true}) })
		records.POST("/broken", func(c *gin.Context) { c.JSON(http.StatusBadRequest, gin.H{"error": "bad"}) })
	}

	return r, svc
}

func getSyncCounters(t *testing.T, r *gin.Engine) map[string]int64 {
	t.Helper()
	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/sync", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var counters map[string]int64
	if err := json.Unmarshal(w.Body.Bytes(), &counters); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	return counters
}

func TestSyncHandler_ConfigReturnsInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     int64
	}{
		{"default", 10 * time.Second, 10000},
		{"custom", 1500 * time.Millisecond, 1500},
		{"disabled", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			h := changes.NewSyncHandler(changes.NewSyncService(newFakeSyncStore()), tt.interval)

			r := gin.New()
			r.GET("/api/admin/sync/config", h.GetConfig)

			w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/sync/config", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}

			var body struct {
				IntervalMS int64 `json:"interval_ms"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if body.IntervalMS != tt.want {
				t.Errorf("expected interval_ms=%d, got %d", tt.want, body.IntervalMS)
			}
		})
	}
}

func TestSyncHandler_SnapshotReturnsAllGroups(t *testing.T) {
	r, _ := setupSyncHandlerRouter(t)

	counters := getSyncCounters(t, r)
	for _, g := range testSyncGroups {
		if _, ok := counters[string(g)]; !ok {
			t.Errorf("missing group %s in response %v", g, counters)
		}
	}
}

func TestSyncHandler_MutatingRequestBumpsGroup(t *testing.T) {
	r, _ := setupSyncHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/records", map[string]any{"x": 1})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	counters := getSyncCounters(t, r)
	if counters[string(changes.SyncGroupRecord)] != 1 {
		t.Errorf("expected record=1, got %d", counters[string(changes.SyncGroupRecord)])
	}
}

func TestSyncHandler_FailedRequestDoesNotBump(t *testing.T) {
	r, _ := setupSyncHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/records/broken", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	counters := getSyncCounters(t, r)
	if counters[string(changes.SyncGroupRecord)] != 0 {
		t.Errorf("expected record=0 after failure, got %d", counters[string(changes.SyncGroupRecord)])
	}
}

func TestSyncHandler_ReadRequestDoesNotBump(t *testing.T) {
	r, _ := setupSyncHandlerRouter(t)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/records", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	counters := getSyncCounters(t, r)
	if counters[string(changes.SyncGroupRecord)] != 0 {
		t.Errorf("expected record=0 after read, got %d", counters[string(changes.SyncGroupRecord)])
	}
}
