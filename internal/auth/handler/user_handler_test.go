package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authhandler "clinic-backend/internal/auth/handler"
	authmodel "clinic-backend/internal/auth/model"
	authservice "clinic-backend/internal/auth/service"
	staffmodel "clinic-backend/internal/staff/model"
	staffservice "clinic-backend/internal/staff/service"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupUserHandlerRouter creates a test router with optional mock auth context.
// If staff is non-nil, a middleware injects the staff and role into the context.
func setupUserHandlerRouter(t *testing.T, staff *staffmodel.ClinicStaff, role authhandler.StaffRole) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := authhandler.NewUserHandler()
	r := gin.New()

	if staff != nil {
		r.Use(func(c *gin.Context) {
			c.Set("staff", *staff)
			c.Set("staff_role", role)
			c.Next()
		})
	}

	r.GET("/api/user/", h.Current)
	r.GET("/api/users/me/", h.Me)
	return r
}

func TestUserHandler_Current_Authenticated(t *testing.T) {
	staff := staffmodel.ClinicStaff{ID: 3, AccountID: "alice", Realname: "张三", Email: "alice@example.com"}
	r := setupUserHandlerRouter(t, &staff, authhandler.RoleStaff)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/user/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got authhandler.UserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.ID != 3 || got.AccountID != "alice" || got.Role != "staff" {
		t.Errorf("unexpected user response: %+v", got)
	}
}

func TestUserHandler_Current_Unauthenticated(t *testing.T) {
	r := setupUserHandlerRouter(t, nil, "")

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/user/", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUserHandler_Me_Authenticated(t *testing.T) {
	staff := staffmodel.ClinicStaff{ID: 5, AccountID: "bob", Realname: "李四", Email: "bob@example.com"}
	r := setupUserHandlerRouter(t, &staff, authhandler.RoleAdmin)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/users/me/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got authhandler.UserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.ID != 5 || got.AccountID != "bob" || got.Role != "admin" {
		t.Errorf("unexpected user response: %+v", got)
	}
}

func TestUserHandler_Me_Anonymous(t *testing.T) {
	r := setupUserHandlerRouter(t, nil, "")

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/users/me/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got authhandler.UserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.ID != 0 || got.AccountID != "" || got.Role != "" {
		t.Errorf("expected empty user, got %+v", got)
	}
}

func TestUserHandler_Current_AdminResponse(t *testing.T) {
	staff := staffmodel.ClinicStaff{ID: 1, AccountID: "admin", Realname: "管理员", Email: "admin@example.com"}
	r := setupUserHandlerRouter(t, &staff, authhandler.RoleAdmin)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/user/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got authhandler.UserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Role != "admin" {
		t.Errorf("expected admin role, got %q", got.Role)
	}
}

type userTestEnv struct {
	handler      *authhandler.UserHandler
	sessionSvc   *authservice.SessionService
	staffSvc     *staffservice.StaffService
	staff        staffmodel.ClinicStaff
	sessionToken string
	optionalAuth gin.HandlerFunc
	cookieName   string
}

func setupUserTestEnv(t *testing.T) *userTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&staffmodel.ClinicStaff{}, &authmodel.AuthSession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	staffSvc := staffservice.NewStaffService(db)
	sessionSvc := authservice.NewSessionService(db, time.Hour)

	staff, err := staffSvc.GetOrCreateByAccountID("integration_user", "测试", "")
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}

	sessionToken, _, err := sessionSvc.Create(staff.ID, "staff", "ST-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	cookieName := "sessionid"
	authCfg := authhandler.AdminAuthConfig{
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		CookieName:     cookieName,
	}
	optionalAuth := authhandler.NewOptionalAdminAuthMiddleware(authCfg)

	return &userTestEnv{
		handler:      authhandler.NewUserHandler(),
		sessionSvc:   sessionSvc,
		staffSvc:     staffSvc,
		staff:        staff,
		sessionToken: sessionToken,
		optionalAuth: optionalAuth,
		cookieName:   cookieName,
	}
}

func TestUserHandler_Me_WithSession(t *testing.T) {
	env := setupUserTestEnv(t)

	r := gin.New()
	r.Use(env.optionalAuth)
	r.GET("/api/users/me/", env.handler.Me)

	req := httptest.NewRequest(http.MethodGet, "/api/users/me/", nil)
	req.AddCookie(&http.Cookie{Name: env.cookieName, Value: env.sessionToken})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got authhandler.UserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.ID != env.staff.ID || got.AccountID != env.staff.AccountID {
		t.Errorf("expected staff %d, got %+v", env.staff.ID, got)
	}
}

func TestUserHandler_Me_NoSession(t *testing.T) {
	env := setupUserTestEnv(t)

	r := gin.New()
	r.Use(env.optionalAuth)
	r.GET("/api/users/me/", env.handler.Me)

	w := testutil.DoRequest(t, r, http.MethodGet, "/api/users/me/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got authhandler.UserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.ID != 0 || got.AccountID != "" {
		t.Errorf("expected anonymous, got %+v", got)
	}
}
