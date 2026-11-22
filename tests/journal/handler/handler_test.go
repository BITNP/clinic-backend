package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"clinic-backend/internal/app"
	authhandler "clinic-backend/internal/auth/handler"
	"clinic-backend/internal/journal"
	journalhandler "clinic-backend/internal/journal/handler"
	journalmodel "clinic-backend/internal/journal/model"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupJournalRouter(t *testing.T, role authhandler.StaffRole) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&journalmodel.Event{}); err != nil {
		t.Fatalf("migrate journal: %v", err)
	}
	svc := journal.NewService(testutil.RawDB(t, db))
	auth := func(c *gin.Context) {
		c.Set("staff_role", role)
		c.Next()
	}
	return app.NewRouter(app.RouterConfig{
		JournalH:  journalhandler.NewHandler(svc),
		AdminAuth: auth,
	}), db
}

func insertJournalEvent(
	t *testing.T,
	db *gorm.DB,
	now time.Time,
	action, entityID, operationID string,
) uint64 {
	t.Helper()
	rawDB := testutil.RawDB(t, db)
	tx, err := rawDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	id, err := journal.InsertTx(context.Background(), tx, journal.EventInput{
		OccurredAt:  now,
		ActorType:   journal.ActorStaff,
		ActorID:     "42",
		Action:      action,
		EntityType:  "record",
		EntityID:    entityID,
		OperationID: operationID,
		Details:     map[string]any{"version": 1, "changes": map[string]any{}},
	})
	if err != nil {
		tx.Rollback()
		t.Fatalf("insert event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit event: %v", err)
	}
	return id
}

func TestJournalListFiltersAndPaginates(t *testing.T) {
	r, db := setupJournalRouter(t, authhandler.RoleAdmin)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	firstID := insertJournalEvent(t, db, now, "record.confirmed", "123", "op-a")
	secondID := insertJournalEvent(t, db, now.Add(time.Minute), "record.updated", "123", "op-b")

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/journal?entity_type=record&entity_id=123&limit=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list journal: %d %s", w.Code, w.Body.String())
	}
	var first struct {
		Items []struct {
			ID      uint64         `json:"id"`
			Action  string         `json:"action"`
			Details map[string]any `json:"details"`
		} `json:"items"`
		NextCursor *uint64 `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != secondID ||
		first.Items[0].Action != "record.updated" || first.NextCursor == nil ||
		*first.NextCursor != secondID {
		t.Fatalf("first page = %+v", first)
	}
	if first.Items[0].Details["version"] != float64(1) {
		t.Fatalf("details = %+v", first.Items[0].Details)
	}

	w = testutil.DoRequest(
		t,
		r,
		http.MethodGet,
		"/api/admin/journal?entity_type=record&entity_id=123&limit=1&cursor="+strconv.FormatUint(*first.NextCursor, 10),
		nil,
	)
	if w.Code != http.StatusOK {
		t.Fatalf("list second page: %d %s", w.Code, w.Body.String())
	}
	var second struct {
		Items []struct {
			ID uint64 `json:"id"`
		} `json:"items"`
		NextCursor *uint64 `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != firstID || second.NextCursor != nil {
		t.Fatalf("second page = %+v", second)
	}
}

func TestJournalListValidatesFiltersAndRequiresAdmin(t *testing.T) {
	r, _ := setupJournalRouter(t, authhandler.RoleAdmin)
	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/journal?from=not-a-time", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid from: %d %s", w.Code, w.Body.String())
	}
	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/journal?actor_type=unknown", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid actor: %d %s", w.Code, w.Body.String())
	}

	r, _ = setupJournalRouter(t, authhandler.RoleStaff)
	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/journal", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("staff access: %d %s", w.Code, w.Body.String())
	}
}
