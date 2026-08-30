package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"clinic-backend/models"
	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

const (
	staffContextKey     = "staff"
	staffRoleContextKey = "staff_role"
)

// StaffRole is the authorization level derived from identity-provider groups.
type StaffRole string

const (
	RoleAdmin StaffRole = "admin"
	RoleStaff StaffRole = "staff"
)

// contextStaff extracts the ClinicStaff from the Gin context.
func contextStaff(c *gin.Context) (models.ClinicStaff, bool) {
	v, ok := c.Get(staffContextKey)
	if !ok {
		return models.ClinicStaff{}, false
	}
	s, ok := v.(models.ClinicStaff)
	return s, ok
}

// contextRole extracts the StaffRole from the Gin context.
func contextRole(c *gin.Context) (StaffRole, bool) {
	v, ok := c.Get(staffRoleContextKey)
	if !ok {
		return "", false
	}
	r, ok := v.(StaffRole)
	return r, ok
}

// RequireStaff aborts with 403 unless the request is authenticated as staff or admin.
func RequireStaff(c *gin.Context) {
	role, ok := contextRole(c)
	if !ok || (role != RoleStaff && role != RoleAdmin) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.Next()
}

// RequireAdmin aborts with 403 unless the request is authenticated as admin.
func RequireAdmin(c *gin.Context) {
	role, ok := contextRole(c)
	if !ok || role != RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.Next()
}

// determineRole maps group names to staff/admin roles using the same rules as Keycloak.
func determineRole(groups []string) StaffRole {
	hasManagement := false
	hasClinic := false
	for _, g := range groups {
		lg := strings.ToLower(g)
		if strings.Contains(lg, "management") {
			hasManagement = true
		}
		if strings.Contains(lg, "clinic") {
			hasClinic = true
		}
	}
	if hasManagement && hasClinic {
		return RoleAdmin
	}
	if hasClinic {
		return RoleStaff
	}
	return ""
}

// extractWorkYears parses group names of the form YYYY-XXXX and returns the
// year for each one. Groups that do not match the format are ignored.
func extractWorkYears(groups []string) []int {
	var years []int
	seen := make(map[int]bool)
	for _, g := range groups {
		year, ok := parseGroupYear(g)
		if !ok || seen[year] {
			continue
		}
		seen[year] = true
		years = append(years, year)
	}
	return years
}

// parseGroupYear extracts the leading year from a group named YYYY-XXXX.
func parseGroupYear(group string) (int, bool) {
	trimmed := strings.TrimSpace(group)
	trimmed = strings.TrimPrefix(trimmed, "/")
	parts := strings.Split(trimmed, "-")
	if len(parts) < 2 || parts[1] == "" {
		return 0, false
	}
	year, err := strconv.Atoi(parts[0])
	if err != nil || year < 1000 || year > 9999 {
		return 0, false
	}
	return year, true
}

// AdminAuthConfig configures the combined session + JWT authentication middleware.
type AdminAuthConfig struct {
	SessionService *services.SessionService
	StaffService   *services.StaffService
	KeycloakAuth   *KeycloakAuthenticator
	CookieName     string
}

// NewAdminAuthMiddleware returns a Gin middleware that accepts either a CAS session
// cookie or a Keycloak Bearer JWT. It populates the existing staff/staff_role context
// keys so that RequireStaff and RequireAdmin continue to work unchanged.
func NewAdminAuthMiddleware(cfg AdminAuthConfig) gin.HandlerFunc {
	m := &adminAuthMiddleware{
		sessionSvc: cfg.SessionService,
		staffSvc:   cfg.StaffService,
		kcAuth:     cfg.KeycloakAuth,
		cookieName: cfg.CookieName,
	}
	return m.handle
}

type adminAuthMiddleware struct {
	sessionSvc *services.SessionService
	staffSvc   *services.StaffService
	kcAuth     *KeycloakAuthenticator
	cookieName string
}

func (m *adminAuthMiddleware) handle(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		m.handleBearer(c, strings.TrimPrefix(auth, "Bearer "))
		return
	}
	m.handleSession(c)
}

func (m *adminAuthMiddleware) handleBearer(c *gin.Context, token string) {
	if m.kcAuth == nil || !m.kcAuth.Configured() {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "bearer authentication not configured"})
		return
	}
	staff, role, err := m.kcAuth.Authenticate(token)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	c.Set(staffContextKey, staff)
	c.Set(staffRoleContextKey, role)
	c.Next()
}

func (m *adminAuthMiddleware) handleSession(c *gin.Context) {
	if m.sessionSvc == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	token, err := c.Cookie(m.cookieName)
	if err != nil || token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	sess, err := m.sessionSvc.Get(token)
	if err != nil {
		if errors.Is(err, services.ErrSessionNotFound) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "session error"})
		return
	}

	staff, err := m.staffSvc.GetByID(sess.StaffID)
	if err != nil {
		if errors.Is(err, services.ErrStaffNotFound) {
			_ = m.sessionSvc.Delete(token)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve staff"})
		return
	}

	if isMutatingMethod(c.Request.Method) {
		csrfHeader := c.GetHeader("X-CSRF-Token")
		if csrfHeader == "" || !m.sessionSvc.ValidateCSRF(token, csrfHeader) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid or missing csrf token"})
			return
		}
	}

	c.Set(staffContextKey, staff)
	c.Set(staffRoleContextKey, StaffRole(sess.Role))
	c.Next()
}

// NewOptionalAdminAuthMiddleware returns a Gin middleware that tries to
// authenticate via Bearer JWT or session cookie but never aborts the request.
// If no credentials are found or they are invalid, the request continues as
// anonymous (no staff/role in context).
func NewOptionalAdminAuthMiddleware(cfg AdminAuthConfig) gin.HandlerFunc {
	m := &adminAuthMiddleware{
		sessionSvc: cfg.SessionService,
		staffSvc:   cfg.StaffService,
		kcAuth:     cfg.KeycloakAuth,
		cookieName: cfg.CookieName,
	}
	return m.optionalHandle
}

func (m *adminAuthMiddleware) optionalHandle(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		m.optionalTryBearer(c, strings.TrimPrefix(auth, "Bearer "))
	} else {
		m.optionalTrySession(c)
	}
	c.Next()
}

func (m *adminAuthMiddleware) optionalTryBearer(c *gin.Context, token string) {
	if m.kcAuth == nil || !m.kcAuth.Configured() {
		return
	}
	staff, role, err := m.kcAuth.Authenticate(token)
	if err != nil {
		return
	}
	c.Set(staffContextKey, staff)
	c.Set(staffRoleContextKey, role)
}

func (m *adminAuthMiddleware) optionalTrySession(c *gin.Context) {
	if m.sessionSvc == nil {
		return
	}

	token, err := c.Cookie(m.cookieName)
	if err != nil || token == "" {
		return
	}

	sess, err := m.sessionSvc.Get(token)
	if err != nil {
		return
	}

	staff, err := m.staffSvc.GetByID(sess.StaffID)
	if err != nil {
		return
	}

	c.Set(staffContextKey, staff)
	c.Set(staffRoleContextKey, StaffRole(sess.Role))
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
