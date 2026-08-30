package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"clinic-backend/handlers"
	"clinic-backend/models"
	"clinic-backend/services"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupCASAuthRouter(t *testing.T, fakeCAS *httptest.Server) (*gin.Engine, *services.SessionService, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.ClinicStaff{}, &models.ClinicStaffWorkyear{}, &models.AuthSession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	staffSvc := services.NewStaffService(db)
	sessionSvc := services.NewSessionService(db, time.Hour)

	var casClient handlers.CASClient
	if fakeCAS != nil {
		casClient = handlers.NewCASClient(fakeCAS.URL, "url", 5*time.Second)
	}

	casHandler := handlers.NewCASAuthHandler(handlers.CASAuthConfig{
		Client:         casClient,
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		BaseURL:        "https://app.example.edu",
		DefaultNext:    "/manage/",
		CookieName:     "sessionid",
		CSRFCookieName: "csrf_token",
		CookieSecure:   true,
		CookieSameSite: http.SameSiteLaxMode,
		SessionTTL:     time.Hour,
	})

	r := gin.New()
	r.GET("/login", casHandler.Login)
	r.GET("/logout", casHandler.Logout)

	adminAuth := handlers.NewAdminAuthMiddleware(handlers.AdminAuthConfig{
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		CookieName:     "sessionid",
	})
	authed := r.Group("/api/admin/fake")
	authed.Use(adminAuth, handlers.RequireStaff)
	{
		authed.GET("", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		authed.POST("", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	}

	return r, sessionSvc, db
}

func TestCASAuthHandler_LoginRedirect(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected CAS request: %s", r.URL)
	}))
	defer fake.Close()

	r, _, _ := setupCASAuthRouter(t, fake)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?next=/manage/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, fake.URL+"/login?service=") {
		t.Errorf("unexpected redirect: %s", loc)
	}
	encodedService := strings.TrimPrefix(loc, fake.URL+"/login?service=")
	service, err := url.QueryUnescape(encodedService)
	if err != nil {
		t.Fatalf("decode service: %v", err)
	}
	u, err := url.Parse(service)
	if err != nil {
		t.Fatalf("parse service: %v", err)
	}
	if u.Query().Get("next") != "/manage/" {
		t.Errorf("expected next=/manage/, got %q", u.Query().Get("next"))
	}
}

func TestCASAuthHandler_LoginCallback(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/serviceValidate" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationSuccess>
    <cas:user>student42</cas:user>
    <cas:attributes>
      <cas:name>Alice Smith</cas:name>
      <cas:groups>/clinic</cas:groups>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	r, _, _ := setupCASAuthRouter(t, fake)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?ticket=ST-123&next=/dashboard", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("unexpected redirect: %s", loc)
	}

	cookies := w.Result().Cookies()
	var hasSession, hasCSRF bool
	for _, c := range cookies {
		if c.Name == "sessionid" && c.Value != "" {
			hasSession = true
			if !c.HttpOnly {
				t.Errorf("session cookie must be HttpOnly")
			}
			if !c.Secure {
				t.Errorf("session cookie must be Secure")
			}
		}
		if c.Name == "csrf_token" && c.Value != "" {
			hasCSRF = true
			if c.HttpOnly {
				t.Errorf("csrf cookie must not be HttpOnly")
			}
		}
	}
	if !hasSession {
		t.Errorf("expected session cookie")
	}
	if !hasCSRF {
		t.Errorf("expected csrf cookie")
	}
}

func TestCASAuthHandler_LoginCallback_RecordsWorkYear(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationSuccess>
    <cas:user>student42</cas:user>
    <cas:attributes>
      <cas:name>Alice Smith</cas:name>
      <cas:groups>2025-clinic</cas:groups>
      <cas:groups>2024-management</cas:groups>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	r, _, db := setupCASAuthRouter(t, fake)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?ticket=ST-123", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}

	var staff models.ClinicStaff
	if err := db.Where("account_id = ?", "student42").First(&staff).Error; err != nil {
		t.Fatalf("load staff: %v", err)
	}

	var years []models.ClinicStaffWorkyear
	if err := db.Where("staff_id = ?", staff.ID).Order("work_year ASC").Find(&years).Error; err != nil {
		t.Fatalf("load work years: %v", err)
	}
	var got []int
	for _, y := range years {
		got = append(got, y.WorkYear)
	}
	if len(got) != 2 || got[0] != 2024 || got[1] != 2025 {
		t.Errorf("work years: got %v, want [2024 2025]", got)
	}
}

func TestCASAuthHandler_LoginNoGroups(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationSuccess>
    <cas:user>student42</cas:user>
    <cas:attributes>
      <cas:name>Alice Smith</cas:name>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	r, _, _ := setupCASAuthRouter(t, fake)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?ticket=ST-123", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCASAuthHandler_LoginInvalidTicket(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationFailure code="INVALID_TICKET">bad ticket</cas:authenticationFailure>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	r, _, _ := setupCASAuthRouter(t, fake)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?ticket=ST-bad", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCASAuthHandler_LoginExternalNext(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("CAS should not be contacted for external next")
	}))
	defer fake.Close()

	r, _, _ := setupCASAuthRouter(t, fake)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?next=https://evil.example", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	encodedService := strings.TrimPrefix(loc, fake.URL+"/login?service=")
	service, err := url.QueryUnescape(encodedService)
	if err != nil {
		t.Fatalf("decode service: %v", err)
	}
	u, err := url.Parse(service)
	if err != nil {
		t.Fatalf("parse service: %v", err)
	}
	if u.Query().Get("next") != "/manage/" {
		t.Errorf("external next should fall back to default, got %q", u.Query().Get("next"))
	}
}

func TestCASAuthHandler_LoginNotConfigured(t *testing.T) {
	r, _, _ := setupCASAuthRouter(t, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCASAuthHandler_Logout(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logout" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		returnURL := r.URL.Query().Get("url")
		if !strings.HasPrefix(returnURL, "https://app.example.edu") {
			t.Errorf("unexpected return url: %s", returnURL)
		}
	}))
	defer fake.Close()

	r, sessionSvc, _ := setupCASAuthRouter(t, fake)

	// Create a session manually.
	token, _, err := sessionSvc.Create(1, string(handlers.RoleStaff), "ST-123")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logout?next=/", nil)
	req.AddCookie(&http.Cookie{Name: "sessionid", Value: token})
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}

	_, err = sessionSvc.Get(token)
	if err == nil {
		t.Errorf("session should be deleted after logout")
	}

	cookies := w.Result().Cookies()
	var sessionCleared bool
	for _, c := range cookies {
		if c.Name == "sessionid" && c.MaxAge < 0 {
			sessionCleared = true
		}
	}
	if !sessionCleared {
		t.Errorf("expected session cookie to be cleared")
	}
}

func TestCASAuthHandler_SessionAuthAndCSRF(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationSuccess>
    <cas:user>student42</cas:user>
    <cas:attributes>
      <cas:name>Alice Smith</cas:name>
      <cas:groups>/clinic</cas:groups>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	r, _, _ := setupCASAuthRouter(t, fake)

	// Login to obtain session and csrf cookies.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?ticket=ST-123", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}

	var sessionValue, csrfValue string
	for _, c := range w.Result().Cookies() {
		if c.Name == "sessionid" {
			sessionValue = c.Value
		}
		if c.Name == "csrf_token" {
			csrfValue = c.Value
		}
	}
	if sessionValue == "" || csrfValue == "" {
		t.Fatalf("expected session and csrf cookies")
	}

	// GET without CSRF should succeed.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/fake", nil)
	req2.AddCookie(&http.Cookie{Name: "sessionid", Value: sessionValue})
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("GET authed request failed: %d %s", w2.Code, w2.Body.String())
	}

	// POST without CSRF should fail.
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/admin/fake", nil)
	req3.AddCookie(&http.Cookie{Name: "sessionid", Value: sessionValue})
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusForbidden {
		t.Errorf("POST without csrf expected 403, got %d", w3.Code)
	}

	// POST with CSRF should succeed.
	w4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/api/admin/fake", nil)
	req4.AddCookie(&http.Cookie{Name: "sessionid", Value: sessionValue})
	req4.Header.Set("X-CSRF-Token", csrfValue)
	r.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("POST with csrf expected 200, got %d: %s", w4.Code, w4.Body.String())
	}
}

func TestCASAuthHandler_SessionInvalidAfterDelete(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationSuccess>
    <cas:user>student42</cas:user>
    <cas:attributes>
      <cas:name>Alice Smith</cas:name>
      <cas:groups>/clinic</cas:groups>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	r, sessionSvc, _ := setupCASAuthRouter(t, fake)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?ticket=ST-123", nil)
	r.ServeHTTP(w, req)

	var sessionValue string
	for _, c := range w.Result().Cookies() {
		if c.Name == "sessionid" {
			sessionValue = c.Value
		}
	}
	_ = sessionSvc.Delete(sessionValue)

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/fake", nil)
	req2.AddCookie(&http.Cookie{Name: "sessionid", Value: sessionValue})
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("deleted session expected 401, got %d", w2.Code)
	}
}
