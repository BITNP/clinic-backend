package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authhandler "clinic-backend/internal/auth/handler"
	roomhandler "clinic-backend/internal/room/handler"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const routeTestAPIKey = "fake-shared-secret"

func TestClientCampusRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fake database: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &roommodel.ClinicRoom{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	mainCampus := roommodel.ClinicCampus{Name: "Main Campus"}
	otherCampus := roommodel.ClinicCampus{Name: "Other Campus"}
	if err := db.Create(&mainCampus).Error; err != nil {
		t.Fatalf("seed main campus: %v", err)
	}
	if err := db.Create(&otherCampus).Error; err != nil {
		t.Fatalf("seed other campus: %v", err)
	}
	mainCampusID := mainCampus.ID
	rooms := []roommodel.ClinicRoom{
		{Name: "Main Normal", Address: "a", Enabled: true, CampusID: &mainCampusID, Type: roommodel.RoomTypeNormal},
		{Name: "Main Dynamic", Address: "b", Enabled: false, CampusID: &mainCampusID, Type: roommodel.RoomTypeDynamic},
		{Name: "Other Room", Address: "c", Enabled: true, CampusID: &otherCampus.ID, Type: roommodel.RoomTypeNormal},
	}
	if err := db.Create(&rooms).Error; err != nil {
		t.Fatalf("seed rooms: %v", err)
	}

	campusSvc := roomservice.NewCampusService(db)
	roomSvc := roomservice.NewRoomService(db)
	router := (routeHandlers{
		campusH:    roomhandler.NewCampusHandler(campusSvc, roomSvc),
		roomH:      roomhandler.NewRoomHandler(roomSvc),
		clientAuth: authhandler.ClientAuthMiddleware(routeTestAPIKey, 5*time.Minute),
	}).router()

	if got := clientCampusesRequest(t, router, "/api/campuses", false); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: expected 401, got %d: %s", got.Code, got.Body.String())
	}

	got := clientCampusesRequest(t, router, "/api/campuses?name=Main&pageSize=10", true)
	if got.Code != http.StatusOK {
		t.Fatalf("list campuses: expected 200, got %d: %s", got.Code, got.Body.String())
	}
	var campusResp struct {
		Items []roommodel.ClinicCampus `json:"items"`
		Total int64                    `json:"total"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &campusResp); err != nil {
		t.Fatalf("decode campuses: %v", err)
	}
	if campusResp.Total != 1 || len(campusResp.Items) != 1 || campusResp.Items[0].ID != mainCampus.ID {
		t.Fatalf("unexpected campuses: %+v", campusResp)
	}

	roomPath := "/api/campuses/" + testutil.Itoa(mainCampus.ID) + "/rooms?enabled=true"
	got = clientCampusesRequest(t, router, roomPath, true)
	if got.Code != http.StatusOK {
		t.Fatalf("list campus rooms: expected 200, got %d: %s", got.Code, got.Body.String())
	}
	var roomResp struct {
		Items []roommodel.ClinicRoom `json:"items"`
		Total int64                  `json:"total"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &roomResp); err != nil {
		t.Fatalf("decode rooms: %v", err)
	}
	if roomResp.Total != 1 || len(roomResp.Items) != 1 || roomResp.Items[0].Name != "Main Normal" {
		t.Fatalf("unexpected rooms: %+v", roomResp)
	}
	if roomResp.Items[0].Campus == nil || roomResp.Items[0].Campus.ID != mainCampus.ID {
		t.Errorf("room campus = %+v, want campus %d", roomResp.Items[0].Campus, mainCampus.ID)
	}

	for path, wantStatus := range map[string]int{
		"/api/campuses/not-an-id/rooms": http.StatusBadRequest,
		"/api/campuses/999/rooms":       http.StatusNotFound,
		"/api/campuses/" + testutil.Itoa(mainCampus.ID) + "/rooms?type=unknown": http.StatusBadRequest,
	} {
		got := clientCampusesRequest(t, router, path, true)
		if got.Code != wantStatus {
			t.Errorf("%s: expected %d, got %d: %s", path, wantStatus, got.Code, got.Body.String())
		}
	}
}

func clientCampusesRequest(t *testing.T, router http.Handler, path string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	if authenticated {
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		path += separator + "username=client-user"
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authenticated {
		date := time.Now().UTC().Format(time.RFC1123)
		req.Header.Set("Date", date)
		req.Header.Set("X-API-KEY", testutil.SignKey(routeTestAPIKey, "client-user", date))
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}
