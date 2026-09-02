package tests

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"clinic-backend/handlers"
	"clinic-backend/models"
	"clinic-backend/services"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupKeycloakTest(t *testing.T) (*rsa.PrivateKey, string, *services.StaffService, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.ClinicStaff{}, &models.ClinicStaffWorkyear{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	staffSvc := services.NewStaffService(db)

	return key, "fake-kid", staffSvc, db
}

func jwksHandler(key *rsa.PrivateKey, kid string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		eBytes := big.NewInt(int64(key.E)).Bytes()
		// Pad exponent to at least 4 bytes for JWK.
		if len(eBytes) < 4 {
			pad := make([]byte, 4-len(eBytes))
			eBytes = append(pad, eBytes...)
		}
		e := base64.RawURLEncoding.EncodeToString(eBytes)
		set := map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": kid,
				"n":   n,
				"e":   e,
				"alg": "RS256",
				"use": "sig",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}
}

func signTestJWT(key *rsa.PrivateKey, kid, issuer, audience, username, name string, groups []string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":                issuer,
		"aud":                audience,
		"sub":                username + "-sub",
		"preferred_username": username,
		"name":               name,
		"email":              username + "@example.com",
		"groups":             groups,
		"iat":                time.Now().Unix(),
		"exp":                time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = kid
	return token.SignedString(key)
}

func TestKeycloakAuthenticator_Authenticate(t *testing.T) {
	key, kid, staffSvc, db := setupKeycloakTest(t)
	fake := httptest.NewServer(jwksHandler(key, kid))
	defer fake.Close()

	a := handlers.NewKeycloakAuthenticator(fake.URL+"/realms/fake", "clinic-backend", staffSvc)
	if !a.Configured() {
		t.Fatal("expected authenticator to be configured")
	}

	token, err := signTestJWT(key, kid, fake.URL+"/realms/fake/", "clinic-backend", "staff01", "Staff One", []string{"2025-clinic"})
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	staff, role, err := a.Authenticate(token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if staff.AccountID != "staff01" {
		t.Errorf("account id: got %q", staff.AccountID)
	}
	if staff.Realname != "Staff One" {
		t.Errorf("realname: got %q", staff.Realname)
	}
	if staff.Email != "staff01@example.com" {
		t.Errorf("email: got %q", staff.Email)
	}
	if role != handlers.RoleStaff {
		t.Errorf("role: got %q", role)
	}

	var count int64
	if err := db.Model(&models.ClinicStaff{}).Where("account_id = ?", "staff01").Count(&count).Error; err != nil {
		t.Fatalf("count staff: %v", err)
	}
	if count != 1 {
		t.Errorf("expected staff row to be created")
	}

	var wyCount int64
	if err := db.Model(&models.ClinicStaffWorkyear{}).Where("staff_id = ? AND work_year = ?", staff.ID, 2025).Count(&wyCount).Error; err != nil {
		t.Fatalf("count work years: %v", err)
	}
	if wyCount != 1 {
		t.Errorf("expected work year 2025 to be recorded")
	}
}

func TestKeycloakAuthenticator_AuthenticateStampsVersion(t *testing.T) {
	key, kid, staffSvc, db := setupKeycloakTest(t)
	fake := httptest.NewServer(jwksHandler(key, kid))
	defer fake.Close()

	a := handlers.NewKeycloakAuthenticator(fake.URL+"/realms/fake", "clinic-backend", staffSvc)
	a.SetStaffVersion(7)

	token, err := signTestJWT(key, kid, fake.URL+"/realms/fake/", "clinic-backend", "staff01", "Staff One", []string{"2025-clinic"})
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	staff, _, err := a.Authenticate(token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	var stored models.ClinicStaff
	if err := db.First(&stored, staff.ID).Error; err != nil {
		t.Fatalf("load staff: %v", err)
	}
	if stored.Version != 7 {
		t.Errorf("staff version after authenticate: got %d, want 7", stored.Version)
	}
}

func TestKeycloakAuthenticator_AuthenticateAdminRole(t *testing.T) {
	key, kid, staffSvc, _ := setupKeycloakTest(t)
	fake := httptest.NewServer(jwksHandler(key, kid))
	defer fake.Close()

	a := handlers.NewKeycloakAuthenticator(fake.URL+"/realms/fake", "clinic-backend", staffSvc)

	token, err := signTestJWT(key, kid, fake.URL+"/realms/fake/", "clinic-backend", "admin01", "Admin One", []string{"/management", "/clinic"})
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	_, role, err := a.Authenticate(token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if role != handlers.RoleAdmin {
		t.Errorf("role: got %q", role)
	}
}

func TestKeycloakAuthenticator_RejectNoGroups(t *testing.T) {
	key, kid, staffSvc, _ := setupKeycloakTest(t)
	fake := httptest.NewServer(jwksHandler(key, kid))
	defer fake.Close()

	a := handlers.NewKeycloakAuthenticator(fake.URL+"/realms/fake", "clinic-backend", staffSvc)

	token, err := signTestJWT(key, kid, fake.URL+"/realms/fake/", "clinic-backend", "user01", "User One", []string{"/other"})
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	_, _, err = a.Authenticate(token)
	if err == nil {
		t.Fatal("expected error for user without clinic groups")
	}
}

func TestCombinedAuth_BearerToken(t *testing.T) {
	key, kid, staffSvc, _ := setupKeycloakTest(t)
	fake := httptest.NewServer(jwksHandler(key, kid))
	defer fake.Close()

	sessionSvc := services.NewSessionService(mustOpenEmptyDB(t), time.Hour)
	kcAuth := handlers.NewKeycloakAuthenticator(fake.URL+"/realms/fake", "clinic-backend", staffSvc)

	r := gin.New()
	adminAuth := handlers.NewAdminAuthMiddleware(handlers.AdminAuthConfig{
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		KeycloakAuth:   kcAuth,
		CookieName:     "sessionid",
	})
	authed := r.Group("/api/admin/fake")
	authed.Use(adminAuth, handlers.RequireStaff)
	{
		authed.GET("", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	}

	token, err := signTestJWT(key, kid, fake.URL+"/realms/fake/", "clinic-backend", "staff01", "Staff One", []string{"/clinic"})
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/fake", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCombinedAuth_BearerNotConfigured(t *testing.T) {
	key, kid, staffSvc, _ := setupKeycloakTest(t)
	fake := httptest.NewServer(jwksHandler(key, kid))
	defer fake.Close()

	sessionSvc := services.NewSessionService(mustOpenEmptyDB(t), time.Hour)
	kcAuth := handlers.NewKeycloakAuthenticator("", "", staffSvc)

	r := gin.New()
	adminAuth := handlers.NewAdminAuthMiddleware(handlers.AdminAuthConfig{
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		KeycloakAuth:   kcAuth,
		CookieName:     "sessionid",
	})
	authed := r.Group("/api/admin/fake")
	authed.Use(adminAuth, handlers.RequireStaff)
	{
		authed.GET("", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	}

	token, err := signTestJWT(key, kid, fake.URL+"/realms/fake/", "clinic-backend", "staff01", "Staff One", []string{"/clinic"})
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/fake", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func mustOpenEmptyDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.AuthSession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestKeycloakMiddleware_NotConfigured(t *testing.T) {
	_, _, staffSvc, _ := setupKeycloakTest(t)
	r := gin.New()
	mw := handlers.NewKeycloakAuthMiddleware("", "", staffSvc)
	authed := r.Group("/api/admin/fake")
	authed.Use(mw, handlers.RequireStaff)
	{
		authed.GET("", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/fake", nil)
	req.Header.Set("Authorization", "Bearer dummy")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}
