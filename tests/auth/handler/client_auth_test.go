package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authhandler "clinic-backend/internal/auth/handler"
	"clinic-backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

const testAPIKey = "fake-shared-secret"

func setupAuthRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	authed := r.Group("/api", authhandler.ClientAuthMiddleware(testAPIKey, 5*time.Minute))
	authed.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": c.GetString("user")})
	})
	return r
}

func TestClientAuth(t *testing.T) {
	now := time.Now().UTC()
	validDate := now.Format(time.RFC1123)
	staleDate := now.Add(-10 * time.Minute).Format(time.RFC1123)
	futureDate := now.Add(10 * time.Minute).Format(time.RFC1123)

	cases := []struct {
		name       string
		date       string
		username   string
		apiKey     string
		wantStatus int
	}{
		{"valid", validDate, "1120221234", testutil.SignKey(testAPIKey, "1120221234", validDate), http.StatusOK},
		{"missing date", "", "1120221234", testutil.SignKey(testAPIKey, "1120221234", validDate), http.StatusUnauthorized},
		{"missing username", validDate, "", testutil.SignKey(testAPIKey, "", validDate), http.StatusUnauthorized},
		{"missing api key", validDate, "1120221234", "", http.StatusUnauthorized},
		{"malformed date", "not-a-date", "1120221234", "deadbeef", http.StatusUnauthorized},
		{"stale date", staleDate, "1120221234", testutil.SignKey(testAPIKey, "1120221234", staleDate), http.StatusUnauthorized},
		{"future date", futureDate, "1120221234", testutil.SignKey(testAPIKey, "1120221234", futureDate), http.StatusUnauthorized},
		{"wrong key", validDate, "1120221234", "0" + testutil.SignKey(testAPIKey, "1120221234", validDate)[1:], http.StatusUnauthorized},
		{"key for different user", validDate, "1120221234", testutil.SignKey(testAPIKey, "9999999999", validDate), http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := setupAuthRouter(t)
			path := "/api/ping?username=" + tc.username
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if tc.date != "" {
				req.Header.Set("Date", tc.date)
			}
			if tc.apiKey != "" {
				req.Header.Set("X-API-KEY", tc.apiKey)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, w.Code, w.Body.String())
			}
		})
	}
}

func TestClientAuth_SetsUserInContext(t *testing.T) {
	r := setupAuthRouter(t)
	date := time.Now().UTC().Format(time.RFC1123)
	username := "1120221234"

	req := httptest.NewRequest(http.MethodGet, "/api/ping?username="+username, nil)
	req.Header.Set("Date", date)
	req.Header.Set("X-API-KEY", testutil.SignKey(testAPIKey, username, date))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		User string `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.User != username {
		t.Errorf("expected user %q, got %q", username, resp.User)
	}
}
