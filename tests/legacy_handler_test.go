package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"clinic-backend/handlers"
	"clinic-backend/models"
	"clinic-backend/services"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupLegacyHandlerRouter(t *testing.T) (*gin.Engine, *gorm.DB, *services.TicketService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.ClinicRoom{},
		&models.ClinicServiceDate{},
		&models.ClinicRecord{},
		&models.ClinicRecordDevice{},
		&models.ClinicRecordArrival{},
		&models.ClinicRecordWorker{},
		&models.ClinicRecordRejection{},
		&models.ClinicRecordReferral{},
		&models.ClinicAnnouncement{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ticketSvc := services.NewTicketService(db, nil)
	serviceDateSvc := services.NewServiceDateService(db, nil)
	roomSvc := services.NewRoomService(db)
	announceSvc := services.NewAnnouncementService(db)
	legacyH := handlers.NewLegacyHandler(ticketSvc, serviceDateSvc, roomSvc, announceSvc)

	r := gin.New()
	authed := r.Group("", handlers.ClientAuthMiddleware(ticketAPIKey, 5*time.Minute))
	{
		// Wechat records
		authed.GET("/api/wechat", legacyH.ListRecords)
		authed.POST("/api/wechat", legacyH.CreateRecord)
		authed.GET("/api/wechat/working", legacyH.WorkingRecord)
		authed.GET("/api/wechat/finished", legacyH.FinishRecords)
		authed.GET("/api/wechat/finish", legacyH.FinishRecords)
		authed.GET("/api/wechat/:id", legacyH.GetRecord)
		authed.PUT("/api/wechat/:id", legacyH.UpdateRecord)
		authed.DELETE("/api/wechat/:id", legacyH.DeleteRecord)

		// Catalog
		authed.GET("/api/campus", legacyH.ListCampus)
		authed.GET("/api/date", legacyH.ListDates)
		authed.GET("/api/date/all", legacyH.ListAllDates)
		authed.GET("/api/announcement", legacyH.ListAnnouncements)
	}

	// TOS is public (no auth)
	r.GET("/api/announcement/toc", legacyH.TOS)
	r.GET("/api/announcement/toc/", legacyH.TOS)

	return r, db, ticketSvc
}

// ── TOS ───────────────────────────────────────────────────────────────────

func TestLegacy_TOS_Success(t *testing.T) {
	r, db, _ := setupLegacyHandlerRouter(t)
	svc := services.NewAnnouncementService(db)
	_, err := svc.Create(services.CreateAnnouncementInput{
		Title:      "Terms",
		Content:    "hello tos",
		Tag:        "tos",
		Brief:      "b",
		ExpireDate: futureDate(30),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/announcement/toc/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["content"] != "hello tos" {
		t.Errorf("expected content 'hello tos', got %v", resp["content"])
	}
	if _, hasTitle := resp["title"]; hasTitle {
		t.Errorf("TOS should return {content} only, not full announcement: %+v", resp)
	}
}

func TestLegacy_TOS_NotFound(t *testing.T) {
	r, _, _ := setupLegacyHandlerRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/announcement/toc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["content"] != "暂无公告" {
		t.Errorf("expected '暂无公告', got %v", resp["content"])
	}
}

// ── Create ────────────────────────────────────────────────────────────────

func TestLegacy_CreateRecord_OldShape(t *testing.T) {
	r, db, _ := setupLegacyHandlerRouter(t)
	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)

	body := map[string]any{
		"realname":         "张三",
		"phone_num":        "13800138000",
		"campus":           "中关村",
		"appointment_time": futureTruncatedDate(7).Format("2006-01-02"),
		"description":      "笔电不开机",
		"model":            "ThinkPad X1",
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodPost, "/api/wechat", body, ""))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// 12 keys exactly like RecordSerializerWechat
	for _, key := range []string{"url", "user", "status", "realname", "phone_num", "campus", "appointment_time", "description", "worker_description", "model", "reject_reason", "password"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing key %q", key)
		}
	}
	// status is int
	status, ok := resp["status"].(float64)
	if !ok {
		t.Fatalf("status should be a number, got %T", resp["status"])
	}
	if int(status) != 1 {
		t.Errorf("expected status 1 (pending), got %v", status)
	}
	// url is /api/wechat/{id}/
	url, _ := resp["url"].(string)
	if len(url) < 13 || url[:12] != "/api/wechat/" || url[len(url)-1:] != "/" {
		t.Errorf("url should be /api/wechat/{id}/, got %q", url)
	}
}

// ── List with DRF envelope ────────────────────────────────────────────────

func TestLegacy_ListRecords_DRFEnvelope(t *testing.T) {
	r, db, svc := setupLegacyHandlerRouter(t)
	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	// Seed a record
	if _, err := svc.Create(services.CreateTicketInput{
		User: "1120221234", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "d",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/wechat", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// DRF envelope keys
	for _, key := range []string{"count", "next", "previous", "results"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing DRF envelope key %q", key)
		}
	}
	count, _ := resp["count"].(float64)
	if count != 1 {
		t.Errorf("expected count 1, got %v", count)
	}
	results, _ := resp["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	rec := results[0].(map[string]any)
	status, _ := rec["status"].(float64)
	if int(status) != 1 {
		t.Errorf("legacy status should be int 1, got %v", status)
	}
}

// ── Working ───────────────────────────────────────────────────────────────

func TestLegacy_Working_Empty(t *testing.T) {
	r, _, _ := setupLegacyHandlerRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/wechat/working", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["count"] != float64(0) {
		t.Errorf("expected count 0, got %v", resp["count"])
	}
}

func TestLegacy_Working_Found(t *testing.T) {
	r, db, svc := setupLegacyHandlerRouter(t)
	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	if _, err := svc.Create(services.CreateTicketInput{
		User: "1120221234", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "d",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/wechat/working", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["count"] != float64(1) {
		t.Errorf("expected count 1, got %v", resp["count"])
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data key, got %+v", resp)
	}
	status, _ := data["status"].(float64)
	if int(status) != 1 {
		t.Errorf("expected legacy status 1, got %v", status)
	}
}

// ── Finish ────────────────────────────────────────────────────────────────

func TestLegacy_Finish_Envelope(t *testing.T) {
	r, db, svc := setupLegacyHandlerRouter(t)
	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	// Seed a completed record
	if _, err := svc.Create(services.CreateTicketInput{
		User: "1120221234", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "d",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Update it to completed via the DB (as the admin flow would)
	db.Model(&models.ClinicRecord{}).Where("user = ?", "1120221234").
		Update("status", models.RecordStatusCompleted)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/wechat/finish", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"count", "next", "previous", "results"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing DRF envelope key %q", key)
		}
	}
}

// ── Get / Update / Delete ────────────────────────────────────────────────

func TestLegacy_GetRecord(t *testing.T) {
	r, db, svc := setupLegacyHandlerRouter(t)
	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	rec, err := svc.Create(services.CreateTicketInput{
		User: "1120221234", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/wechat/"+itoa(rec.ID), nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	status, _ := resp["status"].(float64)
	if int(status) != 1 {
		t.Errorf("expected legacy status 1, got %v", status)
	}
	if resp["realname"] != "r" {
		t.Errorf("expected realname 'r', got %v", resp["realname"])
	}
}

func TestLegacy_UpdateRecord(t *testing.T) {
	r, db, svc := setupLegacyHandlerRouter(t)
	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	rec, err := svc.Create(services.CreateTicketInput{
		User: "1120221234", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodPut, "/api/wechat/"+itoa(rec.ID),
		map[string]any{"realname": "updated"}, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["realname"] != "updated" {
		t.Errorf("expected realname 'updated', got %v", resp["realname"])
	}
	status, _ := resp["status"].(float64)
	if int(status) != 1 {
		t.Errorf("expected legacy status 1, got %v", status)
	}
}

func TestLegacy_DeleteRecord(t *testing.T) {
	r, db, svc := setupLegacyHandlerRouter(t)
	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	rec, err := svc.Create(services.CreateTicketInput{
		User: "1120221234", Realname: "r", PhoneNum: "p", Campus: "中关村",
		AppointmentTime: futureTruncatedDate(7), Description: "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodDelete, "/api/wechat/"+itoa(rec.ID), nil, ""))
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Unauthenticated ───────────────────────────────────────────────────────

func TestLegacy_Unauthenticated(t *testing.T) {
	r, _, _ := setupLegacyHandlerRouter(t)
	for _, path := range []string{"/api/wechat", "/api/wechat/working", "/api/campus", "/api/date", "/api/announcement"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for %s, got %d", path, w.Code)
		}
	}
}

// ── Catalog shapes ────────────────────────────────────────────────────────

func TestLegacy_Campus_Array(t *testing.T) {
	r, db, _ := setupLegacyHandlerRouter(t)
	// Seed two rooms
	db.Create(&models.ClinicRoom{Name: "中关村", Address: "addr1", Enabled: true})
	db.Create(&models.ClinicRoom{Name: "沙河", Address: "addr2", Enabled: true})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/campus", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp []any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected 2 campuses, got %d", len(resp))
	}
	first := resp[0].(map[string]any)
	if _, ok := first["name"]; !ok {
		t.Errorf("expected 'name' key")
	}
	if _, ok := first["address"]; !ok {
		t.Errorf("expected 'address' key")
	}
	if _, ok := first["id"]; ok {
		t.Errorf("legacy campus should not have 'id', got %+v", first)
	}
}

func TestLegacy_Dates_Shape(t *testing.T) {
	r, db, _ := setupLegacyHandlerRouter(t)
	// Seed a room and a service date
	room := models.ClinicRoom{Name: "中关村", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	sd := models.ClinicServiceDate{
		Capacity:  10,
		RoomID:    &room.ID,
		Date:      futureTruncatedDate(7),
		StartTime: futureTruncatedDate(7).Add(10 * time.Hour),
		EndTime:   futureTruncatedDate(7).Add(13 * time.Hour),
		Title:     "正常服务",
	}
	if err := db.Create(&sd).Error; err != nil {
		t.Fatalf("seed service date: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/date", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one date item")
	}
	d := items[0]
	// Check shaped fields
	if _, ok := d["id"]; !ok {
		t.Errorf("missing id")
	}
	if d["date"] != futureTruncatedDate(7).Format("2006-01-02") {
		t.Errorf("date should be YYYY-MM-DD, got %v", d["date"])
	}
	if d["campus"] != "中关村" {
		t.Errorf("campus should be room name, got %v", d["campus"])
	}
	if d["startTime"] != "18:00:00" {
		t.Errorf("startTime should be shifted to clinic timezone (UTC+8), got %v", d["startTime"])
	}
	count, _ := d["count"].(float64)
	if count != 0 {
		t.Errorf("expected count 0 (no bookings), got %v", count)
	}
	if _, ok := d["finish"]; !ok {
		t.Errorf("missing finish field")
	}
	if _, ok := d["working"]; !ok {
		t.Errorf("missing working field")
	}
}

func TestLegacy_Dates_DefaultExcludesPast(t *testing.T) {
	r, db, _ := setupLegacyHandlerRouter(t)
	room := models.ClinicRoom{Name: "中关村", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	pastDate := futureTruncatedDate(-7)
	futureDate := futureTruncatedDate(7)
	for _, d := range []struct {
		date  time.Time
		title string
	}{
		{pastDate, "past"},
		{futureDate, "future"},
	} {
		sd := models.ClinicServiceDate{
			Capacity:  10,
			RoomID:    &room.ID,
			Date:      d.date,
			StartTime: d.date.Add(9 * time.Hour),
			EndTime:   d.date.Add(17 * time.Hour),
			Title:     d.title,
		}
		if err := db.Create(&sd).Error; err != nil {
			t.Fatalf("seed service date %s: %v", d.title, err)
		}
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/date", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 active date, got %d", len(items))
	}
	if items[0]["date"] != futureDate.Format("2006-01-02") {
		t.Errorf("expected future date, got %v", items[0]["date"])
	}
}

func TestLegacy_Dates_AlwaysShiftsToUTC8(t *testing.T) {
	// Build the legacy handler with services configured in UTC to prove the
	// /api/date endpoints ignore the configured timezone and always shift
	// service times into UTC+8 (Asia/Shanghai).
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.ClinicRoom{},
		&models.ClinicServiceDate{},
		&models.ClinicRecord{},
		&models.ClinicRecordDevice{},
		&models.ClinicRecordArrival{},
		&models.ClinicRecordWorker{},
		&models.ClinicRecordRejection{},
		&models.ClinicRecordReferral{},
		&models.ClinicAnnouncement{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ticketSvc := services.NewTicketService(db, time.UTC)
	serviceDateSvc := services.NewServiceDateService(db, time.UTC)
	roomSvc := services.NewRoomService(db)
	announceSvc := services.NewAnnouncementService(db)
	legacyH := handlers.NewLegacyHandler(ticketSvc, serviceDateSvc, roomSvc, announceSvc)

	r := gin.New()
	authed := r.Group("", handlers.ClientAuthMiddleware(ticketAPIKey, 5*time.Minute))
	{
		authed.GET("/api/date", legacyH.ListDates)
		authed.GET("/api/date/all", legacyH.ListAllDates)
	}

	room := models.ClinicRoom{Name: "中关村", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}

	// A date stored at UTC midnight renders as the same calendar day in UTC+8,
	// but 00:30 UTC start time must show as 08:30:00 after the +8 shift.
	base := futureTruncatedDate(7)
	sd := models.ClinicServiceDate{
		Capacity:  10,
		RoomID:    &room.ID,
		Date:      base,
		StartTime: base.Add(30 * time.Minute),
		EndTime:   base.Add(3*time.Hour + 30*time.Minute),
		Title:     "shifted",
	}
	if err := db.Create(&sd).Error; err != nil {
		t.Fatalf("seed service date: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/date/all", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 date, got %d", len(items))
	}
	d := items[0]
	if d["date"] != base.Format("2006-01-02") {
		t.Errorf("date should be %s, got %v", base.Format("2006-01-02"), d["date"])
	}
	if d["startTime"] != "08:30:00" {
		t.Errorf("startTime should be shifted +8 to 08:30:00, got %v", d["startTime"])
	}
	if d["endTime"] != "11:30:00" {
		t.Errorf("endTime should be shifted +8 to 11:30:00, got %v", d["endTime"])
	}

	// The active-only listing must also use UTC+8 for its "today" cutoff:
	// a date on today's Beijing calendar is always included.
	beijingToday := services.DateInLocation(time.Now(), nil)
	sdToday := models.ClinicServiceDate{
		Capacity:  10,
		RoomID:    &room.ID,
		Date:      beijingToday,
		StartTime: beijingToday.Add(9 * time.Hour),
		EndTime:   beijingToday.Add(17 * time.Hour),
		Title:     "today",
	}
	if err := db.Create(&sdToday).Error; err != nil {
		t.Fatalf("seed today service date: %v", err)
	}

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, signedTicketReq(http.MethodGet, "/api/date", nil, ""))
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var active []map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &active); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, it := range active {
		if it["title"] == "today" {
			found = true
		}
	}
	if !found {
		t.Errorf("Beijing-today date should be listed as active, got %v", active)
	}
}

func TestLegacy_DatesAll_IncludesPast(t *testing.T) {
	r, db, _ := setupLegacyHandlerRouter(t)
	room := models.ClinicRoom{Name: "中关村", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	pastDate := futureTruncatedDate(-7)
	futureDate := futureTruncatedDate(7)
	for _, d := range []struct {
		date  time.Time
		title string
	}{
		{pastDate, "past"},
		{futureDate, "future"},
	} {
		sd := models.ClinicServiceDate{
			Capacity:  10,
			RoomID:    &room.ID,
			Date:      d.date,
			StartTime: d.date.Add(9 * time.Hour),
			EndTime:   d.date.Add(17 * time.Hour),
			Title:     d.title,
		}
		if err := db.Create(&sd).Error; err != nil {
			t.Fatalf("seed service date %s: %v", d.title, err)
		}
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/date/all", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 dates, got %d", len(items))
	}
}

func TestLegacy_Announcements_Array(t *testing.T) {
	r, db, _ := setupLegacyHandlerRouter(t)
	svc := services.NewAnnouncementService(db)
	_, err := svc.Create(services.CreateAnnouncementInput{
		Title:      "Notice",
		Content:    "body",
		Tag:        "normal",
		Brief:      "brief",
		ExpireDate: futureDate(30),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTicketReq(http.MethodGet, "/api/announcement", nil, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 announcement, got %d", len(items))
	}
	a := items[0]
	if a["title"] != "Notice" {
		t.Errorf("title mismatch")
	}
	eDate, ok := a["expireDate"].(string)
	if !ok || len(eDate) != 10 {
		t.Errorf("expireDate should be YYYY-MM-DD, got %v", eDate)
	}
	if _, ok := a["id"]; !ok {
		t.Errorf("missing id")
	}
}
