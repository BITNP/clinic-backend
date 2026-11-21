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

	runtimeFile := testutil.WriteRuntimeSettingsFile(t, testutil.RuntimeSettingsValues())
	svc := configservice.NewConfigServiceWithRuntimeSettingsFile(testutil.RawDB(t, db), runtimeFile)
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
		"unit":         " days ",
		"hint":         " Maximum advance booking window. ",
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
	if item.RoleLevel != configmodel.ConfigRoleLevelAdmin || item.WriteOnly {
		t.Errorf("created item access = %q, %t; want admin, false", item.RoleLevel, item.WriteOnly)
	}
	if item.Unit != "days" || item.Hint != "Maximum advance booking window." {
		t.Errorf("created item presentation = %q, %q", item.Unit, item.Hint)
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
	if catalog.Items[0].Unit != "days" || catalog.Items[0].Hint != "Maximum advance booking window." {
		t.Errorf("catalog item presentation = %q, %q", catalog.Items[0].Unit, catalog.Items[0].Hint)
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
		"unit":         "",
		"hint":         "",
		"is_required":  false,
		"writeonly":    false,
		"sort_order":   4,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update item: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	item = configmodel.ClinicConfigItem{}
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode updated item: %v", err)
	}
	if item.Value != "false" || item.ValueType != configmodel.ConfigValueTypeBoolean {
		t.Errorf("updated item = %+v", item)
	}
	if item.RoleLevel != configmodel.ConfigRoleLevelAdmin || item.WriteOnly {
		t.Errorf("updated item access = %q, %t; want admin, false", item.RoleLevel, item.WriteOnly)
	}
	if item.Unit != "" || item.Hint != "" {
		t.Errorf("cleared item presentation = %q, %q", item.Unit, item.Hint)
	}
	if strings.Contains(w.Body.String(), `"unit"`) || strings.Contains(w.Body.String(), `"hint"`) {
		t.Errorf("empty presentation metadata was not omitted: %s", w.Body.String())
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

func TestConfigHandler_WriteOnlyValueIsOmitted(t *testing.T) {
	r, svc, _ := setupConfigHandlerRouter(t, authhandler.RoleSuperAdmin)
	group, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "secrets",
		DisplayName: "Secrets",
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/items", map[string]any{
		"name":         "api_secret",
		"value":        "initial-secret",
		"value_type":   "string",
		"display_name": "API Secret",
		"role_level":   "superadmin",
		"writeonly":    true,
		"group_id":     group.ID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create write-only item: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "initial-secret") || strings.Contains(w.Body.String(), `"value":`) {
		t.Fatalf("create response exposed write-only value: %s", w.Body)
	}
	var created configmodel.ClinicConfigItem
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created item: %v", err)
	}
	stored, err := svc.GetItem(created.ID)
	if err != nil {
		t.Fatalf("get internally stored item: %v", err)
	}
	if stored.Value != "initial-secret" || !stored.WriteOnly {
		t.Fatalf("stored write-only item = %+v", stored)
	}

	path := "/api/admin/config/items/" + testutil.Itoa(created.ID)
	w = testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list catalog: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "initial-secret") || strings.Contains(w.Body.String(), `"value":`) {
		t.Fatalf("catalog exposed write-only value: %s", w.Body)
	}

	w = testutil.DoRequest(t, r, http.MethodGet, path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get write-only item: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "initial-secret") || strings.Contains(w.Body.String(), `"value":`) {
		t.Fatalf("get response exposed write-only value: %s", w.Body)
	}

	w = testutil.DoRequest(t, r, http.MethodPut, path, map[string]any{"value": "updated-secret"})
	if w.Code != http.StatusOK {
		t.Fatalf("update write-only item: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "updated-secret") || strings.Contains(w.Body.String(), `"value":`) {
		t.Fatalf("update response exposed write-only value: %s", w.Body)
	}
	stored, err = svc.GetItem(created.ID)
	if err != nil || stored.Value != "updated-secret" {
		t.Fatalf("internally stored value = %q, error = %v", stored.Value, err)
	}

	w = testutil.DoRequest(t, r, http.MethodDelete, path, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete write-only item: %d %s", w.Code, w.Body)
	}
}

func TestConfigHandler_TaggerAPIKeyStaysWriteOnly(t *testing.T) {
	r, svc, db := setupConfigHandlerRouter(t, authhandler.RoleAdmin)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("seed runtime settings: %v", err)
	}

	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "TAGGER_LLM_API_KEY").First(&item).Error; err != nil {
		t.Fatalf("get tagger API key item: %v", err)
	}
	value := "legacy-provider-key"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); err != nil {
		t.Fatalf("seed tagger API key: %v", err)
	}
	path := "/api/admin/config/items/" + testutil.Itoa(item.ID)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list catalog: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "legacy-provider-key") {
		t.Fatalf("catalog exposed tagger API key: %s", w.Body)
	}

	w = testutil.DoRequest(t, r, http.MethodPut, path, map[string]any{"value": "replacement-provider-key"})
	if w.Code != http.StatusOK {
		t.Fatalf("update tagger API key: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "replacement-provider-key") || strings.Contains(w.Body.String(), `"value":`) {
		t.Fatalf("update response exposed tagger API key: %s", w.Body)
	}
	stored, err := svc.GetItem(item.ID)
	if err != nil || stored.Value != "replacement-provider-key" {
		t.Fatalf("stored tagger API key = %q, error = %v", stored.Value, err)
	}

	w = testutil.DoRequest(t, r, http.MethodPut, path, map[string]any{"writeonly": false})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("declassify tagger API key: %d %s", w.Code, w.Body)
	}
	w = testutil.DoRequest(t, r, http.MethodDelete, path, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete built-in tagger API key: %d %s", w.Code, w.Body)
	}
}

func TestConfigHandler_MessageGatewayTokenStaysWriteOnly(t *testing.T) {
	r, svc, db := setupConfigHandlerRouter(t, authhandler.RoleAdmin)
	if err := svc.EnsureRuntimeSettings(); err != nil {
		t.Fatalf("seed runtime settings: %v", err)
	}

	var item configmodel.ClinicConfigItem
	if err := db.Where("name = ?", "MESSAGE_GATEWAY_ACCOUNT_TOKEN").First(&item).Error; err != nil {
		t.Fatalf("get gateway token item: %v", err)
	}
	value := "legacy-gateway-token"
	if _, err := svc.UpdateItem(item.ID, configservice.UpdateItemInput{Value: &value}); err != nil {
		t.Fatalf("seed gateway token: %v", err)
	}
	path := "/api/admin/config/items/" + testutil.Itoa(item.ID)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list catalog: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "legacy-gateway-token") {
		t.Fatalf("catalog exposed gateway token: %s", w.Body)
	}

	w = testutil.DoRequest(t, r, http.MethodPut, path, map[string]any{"value": "replacement-gateway-token"})
	if w.Code != http.StatusOK {
		t.Fatalf("update gateway token: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "replacement-gateway-token") || strings.Contains(w.Body.String(), `"value":`) {
		t.Fatalf("update response exposed gateway token: %s", w.Body)
	}
	stored, err := svc.GetItem(item.ID)
	if err != nil || stored.Value != "replacement-gateway-token" {
		t.Fatalf("stored gateway token = %q, error = %v", stored.Value, err)
	}

	w = testutil.DoRequest(t, r, http.MethodPut, path, map[string]any{"writeonly": false})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("declassify gateway token: %d %s", w.Code, w.Body)
	}
	w = testutil.DoRequest(t, r, http.MethodDelete, path, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete built-in gateway token: %d %s", w.Code, w.Body)
	}
}

func TestConfigHandler_AdminItemRoleScope(t *testing.T) {
	r, svc, _ := setupConfigHandlerRouter(t, authhandler.RoleAdmin)
	group, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "scope",
		DisplayName: "Scope",
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	createItem := func(name string, roleLevel configmodel.ConfigRoleLevel) configmodel.ClinicConfigItem {
		t.Helper()
		item, err := svc.CreateItem(configservice.CreateItemInput{
			Name:        name,
			Value:       "value",
			ValueType:   configmodel.ConfigValueTypeString,
			DisplayName: name,
			RoleLevel:   roleLevel,
			GroupID:     group.ID,
		})
		if err != nil {
			t.Fatalf("create %s item: %v", roleLevel, err)
		}
		return item
	}
	adminItem := createItem("admin_item", configmodel.ConfigRoleLevelAdmin)
	staffItem := createItem("staff_item", configmodel.ConfigRoleLevelStaff)
	createItem("superadmin_item", configmodel.ConfigRoleLevelSuperAdmin)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list admin catalog: %d %s", w.Code, w.Body)
	}
	var catalog configservice.Catalog
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode admin catalog: %v", err)
	}
	if len(catalog.Items) != 1 || catalog.Items[0].ID != adminItem.ID {
		t.Errorf("admin catalog items = %+v, want only item %d", catalog.Items, adminItem.ID)
	}

	staffPath := "/api/admin/config/items/" + testutil.Itoa(staffItem.ID)
	adminPath := "/api/admin/config/items/" + testutil.Itoa(adminItem.ID)
	if w := testutil.DoRequest(t, r, http.MethodGet, staffPath, nil); w.Code != http.StatusNotFound {
		t.Fatalf("admin get staff item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodPut, staffPath, map[string]any{"value": "changed"}); w.Code != http.StatusNotFound {
		t.Fatalf("admin update staff item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodPut, adminPath, map[string]any{"role_level": "staff"}); w.Code != http.StatusForbidden {
		t.Fatalf("admin change admin item level: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/items", map[string]any{
		"name": "forbidden_item", "value_type": "string", "display_name": "Forbidden",
		"role_level": "staff", "group_id": group.ID,
	}); w.Code != http.StatusForbidden {
		t.Fatalf("admin create staff item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/items", map[string]any{
		"name": "admin_created", "value_type": "string", "display_name": "Admin Created",
		"group_id": group.ID,
	}); w.Code != http.StatusCreated {
		t.Fatalf("admin create admin item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodDelete, staffPath, nil); w.Code != http.StatusNotFound {
		t.Fatalf("admin delete staff item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodDelete, adminPath, nil); w.Code != http.StatusNoContent {
		t.Fatalf("admin delete admin item: %d %s", w.Code, w.Body)
	}
}

func TestConfigHandler_SuperAdminItemRoleScope(t *testing.T) {
	r, svc, _ := setupConfigHandlerRouter(t, authhandler.RoleSuperAdmin)
	group, err := svc.CreateGroup(configservice.CreateGroupInput{
		Name:        "scope",
		DisplayName: "Scope",
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	staffItem, err := svc.CreateItem(configservice.CreateItemInput{
		Name: "staff_item", Value: "value", ValueType: configmodel.ConfigValueTypeString,
		DisplayName: "Staff Item", RoleLevel: configmodel.ConfigRoleLevelStaff, GroupID: group.ID,
	})
	if err != nil {
		t.Fatalf("create staff item: %v", err)
	}
	superItem, err := svc.CreateItem(configservice.CreateItemInput{
		Name: "super_item", Value: "value", ValueType: configmodel.ConfigValueTypeString,
		DisplayName: "Super Item", RoleLevel: configmodel.ConfigRoleLevelSuperAdmin, GroupID: group.ID,
	})
	if err != nil {
		t.Fatalf("create superadmin item: %v", err)
	}

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/admin/config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list superadmin catalog: %d %s", w.Code, w.Body)
	}
	var catalog configservice.Catalog
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode superadmin catalog: %v", err)
	}
	if len(catalog.Items) != 2 {
		t.Errorf("superadmin catalog item count = %d, want 2", len(catalog.Items))
	}

	staffPath := "/api/admin/config/items/" + testutil.Itoa(staffItem.ID)
	superPath := "/api/admin/config/items/" + testutil.Itoa(superItem.ID)
	if w := testutil.DoRequest(t, r, http.MethodGet, staffPath, nil); w.Code != http.StatusOK {
		t.Fatalf("superadmin get staff item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodGet, superPath, nil); w.Code != http.StatusOK {
		t.Fatalf("superadmin get superadmin item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodPut, staffPath, map[string]any{"value": "updated"}); w.Code != http.StatusOK {
		t.Fatalf("superadmin update staff item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodPost, "/api/admin/config/items", map[string]any{
		"name": "super_created", "value_type": "string", "display_name": "Super Created",
		"role_level": "superadmin", "group_id": group.ID,
	}); w.Code != http.StatusCreated {
		t.Fatalf("superadmin create superadmin item: %d %s", w.Code, w.Body)
	}
	if w := testutil.DoRequest(t, r, http.MethodDelete, superPath, nil); w.Code != http.StatusNoContent {
		t.Fatalf("superadmin delete superadmin item: %d %s", w.Code, w.Body)
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
		"name":         "bad_role",
		"value_type":   "string",
		"display_name": "Bad Role",
		"role_level":   "guest",
		"group_id":     group.ID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid role level: expected 400, got %d: %s", w.Code, w.Body.String())
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
