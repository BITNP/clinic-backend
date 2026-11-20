package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"clinic-backend/internal/app"
	authhandler "clinic-backend/internal/auth/handler"
	"clinic-backend/internal/changes"
	confighandler "clinic-backend/internal/config/handler"
	configmodel "clinic-backend/internal/config/model"
	configservice "clinic-backend/internal/config/service"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupConfigHandlerRouter(t *testing.T, role authhandler.StaffRole) (*gin.Engine, *configservice.ConfigService, *gorm.DB) {
	return setupConfigHandlerRouterWithSync(t, role, nil)
}

func setupConfigHandlerRouterWithSync(t *testing.T, role authhandler.StaffRole, syncSvc *changes.SyncService) (*gin.Engine, *configservice.ConfigService, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fake database: %v", err)
	}
	if err := db.AutoMigrate(&configmodel.ClinicConfigGroup{}, &configmodel.ClinicConfigItem{}); err != nil {
		t.Fatalf("migrate config models: %v", err)
	}

	svc := configservice.NewConfigService(testutil.RawDB(t, db))
	h := confighandler.NewConfigHandler(svc)
	auth := func(c *gin.Context) {
		if role == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Set("staff_role", role)
		c.Next()
	}
	r := app.NewRouter(app.RouterConfig{
		ConfigH:   h,
		AdminAuth: auth,
		SyncSvc:   syncSvc,
	})
	return r, svc, db
}

type configSyncStore struct{ counters map[string]int64 }

func (s *configSyncStore) Incr(_ context.Context, key string) (int64, error) {
	s.counters[key]++
	return s.counters[key], nil
}

func (s *configSyncStore) Get(_ context.Context, keys []string) (map[string]int64, error) {
	values := make(map[string]int64, len(keys))
	for _, key := range keys {
		values[key] = s.counters[key]
	}
	return values, nil
}

func TestConfigRoutesBumpSyncOnlyOnSuccessfulWrites(t *testing.T) {
	syncSvc := changes.NewSyncService(&configSyncStore{counters: make(map[string]int64)})
	r, _, _ := setupConfigHandlerRouterWithSync(t, authhandler.RoleAdmin, syncSvc)
	check := func(want int64) {
		t.Helper()
		snapshot, err := syncSvc.Snapshot(context.Background())
		if err != nil || snapshot[changes.SyncGroupConfig] != want {
			t.Fatalf("config counter = %d, want %d (error %v)", snapshot[changes.SyncGroupConfig], want, err)
		}
	}
	check(0)
	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/groups", map[string]any{"name": "test", "display_name": "Test"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", w.Code, w.Body)
	}
	var group configmodel.ClinicConfigGroup
	if err := json.Unmarshal(w.Body.Bytes(), &group); err != nil {
		t.Fatal(err)
	}
	check(1)
	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("read: %d", w.Code)
	}
	w = testutil.DoRequest(t, r, http.MethodPut, "/api/admin/config/groups/999", map[string]any{"display_name": "Missing"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("failed update: %d", w.Code)
	}
	check(1)
	w = testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/items", map[string]any{"name": "test", "display_name": "Test", "value": "1", "value_type": "int", "group_id": group.ID})
	if w.Code != http.StatusCreated {
		t.Fatalf("create item: %d %s", w.Code, w.Body)
	}
	var item configmodel.ClinicConfigItem
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	check(2)
	w = testutil.DoRequest(t, r, http.MethodPut, "/api/admin/config/items/"+testutil.Itoa(item.ID), map[string]any{"value": "2"})
	if w.Code != http.StatusOK {
		t.Fatalf("update item: %d %s", w.Code, w.Body)
	}
	check(3)
	w = testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/config/items/"+testutil.Itoa(item.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete item: %d", w.Code)
	}
	check(4)
}

func TestConfigHandler_CRUDAndFlatCatalog(t *testing.T) {
	r, _, _ := setupConfigHandlerRouter(t, authhandler.RoleAdmin)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/groups", map[string]any{
		"name":         "booking",
		"display_name": "Booking",
		"sort_order":   1,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create group: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var group configmodel.ClinicConfigGroup
	if err := json.Unmarshal(w.Body.Bytes(), &group); err != nil {
		t.Fatalf("decode group: %v", err)
	}

	w = testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/items", map[string]any{
		"name":         "max_booking_days",
		"value":        "007",
		"value_type":   "int",
		"display_name": "Maximum Booking Days",
		"is_required":  true,
		"group_id":     group.ID,
		"sort_order":   2,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create item: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var item configmodel.ClinicConfigItem
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if item.Value != "7" || item.GroupID != group.ID {
		t.Errorf("created item = %+v", item)
	}

	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list catalog: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"group":`) {
		t.Errorf("catalog item contains nested group: %s", w.Body.String())
	}
	var catalog configservice.Catalog
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(catalog.Groups) != 1 || len(catalog.Items) != 1 {
		t.Fatalf("catalog lengths = %d groups, %d items", len(catalog.Groups), len(catalog.Items))
	}
	if catalog.Items[0].GroupID != group.ID {
		t.Errorf("catalog item group_id = %d, want %d", catalog.Items[0].GroupID, group.ID)
	}

	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config/groups/"+testutil.Itoa(group.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get group: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config/items/"+testutil.Itoa(item.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get item: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = testutil.DoRequest(t, r, http.MethodPut, "/api/admin/config/groups/"+testutil.Itoa(group.ID), map[string]any{
		"display_name": "Booking Settings",
		"sort_order":   3,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update group: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = testutil.DoRequest(t, r, http.MethodPut, "/api/admin/config/items/"+testutil.Itoa(item.ID), map[string]any{
		"value":        "false",
		"value_type":   "boolean",
		"display_name": "Enabled",
		"is_required":  false,
		"sort_order":   4,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update item: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode updated item: %v", err)
	}
	if item.Value != "false" || item.ValueType != configmodel.ConfigValueTypeBoolean {
		t.Errorf("updated item = %+v", item)
	}

	w = testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/config/items/"+testutil.Itoa(item.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete item: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	w = testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/config/groups/"+testutil.Itoa(group.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete group: expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfigHandler_ValidationAndConflicts(t *testing.T) {
	r, svc, _ := setupConfigHandlerRouter(t, authhandler.RoleAdmin)

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/groups", map[string]any{
		"name": "missing_display",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing display name: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	group, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "general",
		DisplayName: "General",
	})
	if err != nil {
		t.Fatalf("seed group: %v", err)
	}
	w = testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/groups", map[string]any{
		"name":         "general",
		"display_name": "Duplicate",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate group: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	w = testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/items", map[string]any{
		"name":         "bad_type",
		"value_type":   "date",
		"display_name": "Bad Type",
		"group_id":     group.ID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid value type: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	w = testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/items", map[string]any{
		"name":         "missing_group",
		"value_type":   "string",
		"display_name": "Missing Group",
		"group_id":     999,
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing group: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	item, err := svc.CreateItem(configservice.CreateItemInput{
		Name:        "enabled",
		Value:       "true",
		ValueType:   configmodel.ConfigValueTypeBoolean,
		DisplayName: "Enabled",
		GroupID:     group.ID,
	})
	if err != nil {
		t.Fatalf("seed item: %v", err)
	}
	w = testutil.DoRequest(t, r, http.MethodDelete, "/api/admin/config/groups/"+testutil.Itoa(group.ID), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("delete non-empty group: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config/items/"+testutil.Itoa(item.ID+100), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing item: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config/groups/not-an-id", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid id: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfigHandler_UpdateKnownRuntimeSetting(t *testing.T) {
	r, svc, db := setupConfigHandlerRouter(t, authhandler.RoleAdmin)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("seed runtime settings: %v", err)
	}

	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "RECORD_ACTION_WINDOW").First(&item).Error; err != nil {
		t.Fatalf("get action window setting: %v", err)
	}
	path := "/api/admin/config/items/" + testutil.Itoa(item.ID)

	w := testutil.DoRequest(t, r, http.MethodPut, path, map[string]any{"value": "later"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid duration: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	w = testutil.DoRequest(t, r, http.MethodPut, path, map[string]any{"value": "2m"})
	if w.Code != http.StatusOK {
		t.Fatalf("valid duration: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode updated item: %v", err)
	}
	if item.Value != "2m" {
		t.Errorf("value = %q, want 2m", item.Value)
	}
}

func TestConfigHandler_Authorization(t *testing.T) {
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/config"},
		{http.MethodGet, "/api/admin/config/groups/1"},
		{http.MethodPost, "/api/admin/config/groups"},
		{http.MethodPut, "/api/admin/config/groups/1"},
		{http.MethodDelete, "/api/admin/config/groups/1"},
		{http.MethodGet, "/api/admin/config/items/1"},
		{http.MethodPost, "/api/admin/config/items"},
		{http.MethodPut, "/api/admin/config/items/1"},
		{http.MethodDelete, "/api/admin/config/items/1"},
	}

	for _, role := range []struct {
		name string
		role authhandler.StaffRole
		want int
	}{
		{name: "unauthenticated", role: "", want: http.StatusUnauthorized},
		{name: "staff", role: authhandler.RoleStaff, want: http.StatusForbidden},
	} {
		t.Run(role.name, func(t *testing.T) {
			r, _, _ := setupConfigHandlerRouter(t, role.role)
			for _, route := range routes {
				t.Run(route.method+" "+route.path, func(t *testing.T) {
					var body any
					if route.method == http.MethodPost || route.method == http.MethodPut {
						body = map[string]any{}
					}
					w := testutil.DoRequest(t, r, route.method, route.path, body)
					if w.Code != role.want {
						t.Errorf("expected %d, got %d: %s", role.want, w.Code, w.Body.String())
					}
				})
			}
		})
	}

	for _, role := range []authhandler.StaffRole{authhandler.RoleAdmin, authhandler.RoleSuperAdmin} {
		t.Run(string(role), func(t *testing.T) {
			r, _, _ := setupConfigHandlerRouter(t, role)
			w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
