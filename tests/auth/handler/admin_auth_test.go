package handler_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	authhandler "clinic-backend/internal/auth/handler"

	"github.com/gin-gonic/gin"
)

func TestExtractWorkYears(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		want   []int
	}{
		{name: "single year group", groups: []string{"2025-clinic"}, want: []int{2025}},
		{name: "slash prefixed year group", groups: []string{"/2024-management"}, want: []int{2024}},
		{name: "multiple year groups", groups: []string{"2025-clinic", "2024-management"}, want: []int{2025, 2024}},
		{name: "dedupe duplicate years", groups: []string{"2025-clinic", "2025-management"}, want: []int{2025}},
		{name: "no year format", groups: []string{"/clinic", "/management"}, want: nil},
		{name: "no dash", groups: []string{"2025"}, want: nil},
		{name: "non-numeric year", groups: []string{"year-clinic"}, want: nil},
		{name: "short year", groups: []string{"25-clinic"}, want: nil},
		{name: "out of range year", groups: []string{"123-clinic", "10000-clinic"}, want: nil},
		{name: "empty department", groups: []string{"2025-"}, want: nil},
		{name: "empty groups", groups: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := authhandler.ExtractWorkYears(tt.groups)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractWorkYears(%v) = %v, want %v", tt.groups, got, tt.want)
			}
		})
	}
}

func TestRoleMiddlewareAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		role      authhandler.StaffRole
		staffCode int
		adminCode int
	}{
		{name: "staff", role: authhandler.RoleStaff, staffCode: http.StatusNoContent, adminCode: http.StatusForbidden},
		{name: "admin", role: authhandler.RoleAdmin, staffCode: http.StatusNoContent, adminCode: http.StatusNoContent},
		{name: "superadmin", role: authhandler.RoleSuperAdmin, staffCode: http.StatusNoContent, adminCode: http.StatusNoContent},
		{name: "unknown", role: "", staffCode: http.StatusForbidden, adminCode: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roleMiddlewareStatus(tt.role, authhandler.RequireStaff); got != tt.staffCode {
				t.Errorf("RequireStaff status: got %d, want %d", got, tt.staffCode)
			}
			if got := roleMiddlewareStatus(tt.role, authhandler.RequireAdmin); got != tt.adminCode {
				t.Errorf("RequireAdmin status: got %d, want %d", got, tt.adminCode)
			}
		})
	}
}

func roleMiddlewareStatus(role authhandler.StaffRole, middleware gin.HandlerFunc) int {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("staff_role", role)
	})
	r.Use(middleware)
	r.GET("/", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	return w.Code
}
