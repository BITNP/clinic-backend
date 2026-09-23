package tests

import (
	"encoding/json"
	"net/http"
	"testing"

	"clinic-backend/handlers"
	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

// setupSyncHandlerRouter wires the sync handler behind a fake staff role and a
// write group carrying the bump middleware, plus one endpoint that fails.
func setupSyncHandlerRouter(t *testing.T) (*gin.Engine, *services.SyncService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	svc := services.NewSyncService(newFakeSyncStore())
	h := handlers.NewSyncHandler(svc)

	r := gin.New()

	syncAdmin := r.Group("/api/admin/sync")
	syncAdmin.Use(func(c *gin.Context) {
		c.Set("staff_role", handlers.RoleStaff)
		c.Next()
	})
	{
		syncAdmin.GET("", h.Get)
	}

	records := r.Group("/api/admin/records")
	records.Use(handlers.SyncBumpMiddleware(svc, services.SyncGroupRecord))
	{
		records.GET("", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": []any{}}) })
		records.POST("", func(c *gin.Context) { c.JSON(http.StatusCreated, gin.H{"ok": true}) })
		records.POST("/broken", func(c *gin.Context) { c.JSON(http.StatusBadRequest, gin.H{"error": "bad"}) })
	}

	return r, svc
}

func getSyncCounters(t *testing.T, r *gin.Engine) map[string]int64 {
	t.Helper()
	w := doRequest(t, r, http.MethodGet, "/api/admin/sync", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var counters map[string]int64
	if err := json.Unmarshal(w.Body.Bytes(), &counters); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	return counters
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

	w := doRequest(t, r, http.MethodPost, "/api/admin/records", map[string]any{"x": 1})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	counters := getSyncCounters(t, r)
	if counters[string(services.SyncGroupRecord)] != 1 {
		t.Errorf("expected record=1, got %d", counters[string(services.SyncGroupRecord)])
	}
}

func TestSyncHandler_FailedRequestDoesNotBump(t *testing.T) {
	r, _ := setupSyncHandlerRouter(t)

	w := doRequest(t, r, http.MethodPost, "/api/admin/records/broken", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	counters := getSyncCounters(t, r)
	if counters[string(services.SyncGroupRecord)] != 0 {
		t.Errorf("expected record=0 after failure, got %d", counters[string(services.SyncGroupRecord)])
	}
}

func TestSyncHandler_ReadRequestDoesNotBump(t *testing.T) {
	r, _ := setupSyncHandlerRouter(t)

	w := doRequest(t, r, http.MethodGet, "/api/admin/records", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	counters := getSyncCounters(t, r)
	if counters[string(services.SyncGroupRecord)] != 0 {
		t.Errorf("expected record=0 after read, got %d", counters[string(services.SyncGroupRecord)])
	}
}
