package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

// CASAuthConfig configures the CAS login/logout handlers.
type CASAuthConfig struct {
	Client         CASClient
	SessionService *services.SessionService
	StaffService   *services.StaffService
	BaseURL        string
	DefaultNext    string
	CookieName     string
	CSRFCookieName string
	CookieSecure   bool
	CookieSameSite http.SameSite
	SessionTTL     time.Duration
}

// CASAuthHandler implements GET /login and GET /logout.
type CASAuthHandler struct {
	cfg CASAuthConfig
}

// NewCASAuthHandler creates a CAS auth handler.
func NewCASAuthHandler(cfg CASAuthConfig) *CASAuthHandler {
	if cfg.DefaultNext == "" {
		cfg.DefaultNext = "/manage/"
	}
	if cfg.CSRFCookieName == "" {
		cfg.CSRFCookieName = "csrf_token"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &CASAuthHandler{cfg: cfg}
}

// Login initiates CAS authentication or handles the CAS callback.
func (h *CASAuthHandler) Login(c *gin.Context) {
	if h.cfg.Client == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "cas not configured"})
		return
	}

	// Best-effort cleanup of expired sessions on each login request.
	_ = h.cfg.SessionService.DeleteExpired()

	next := h.validNext(c.Query("next"))
	service := h.serviceURL(next)

	ticket := c.Query("ticket")
	if ticket == "" {
		c.Redirect(http.StatusFound, h.cfg.Client.LoginURL(service))
		return
	}

	attrs, err := h.cfg.Client.ValidateTicket(ticket, service)
	if err != nil {
		if errors.Is(err, ErrCASTicketInvalid) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cas ticket validation failed"})
			return
		}
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "cas service unavailable"})
		return
	}

	role := determineRole(attrs.Groups)
	if role == "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient group membership"})
		return
	}

	staff, err := h.cfg.StaffService.GetOrCreateByAccountID(attrs.User, attrs.Realname, "")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve staff"})
		return
	}

	if err := h.cfg.StaffService.UpdateRole(staff.ID, string(role)); err != nil {
		log.Printf("warning: failed to persist role %s for staff %d: %v", role, staff.ID, err)
	}

	if err := h.cfg.StaffService.EnsureWorkYears(staff.ID, extractWorkYears(attrs.Groups)); err != nil {
		log.Printf("warning: failed to record work years for staff %d: %v", staff.ID, err)
	}

	sessionToken, csrfToken, err := h.cfg.SessionService.Create(staff.ID, string(role), ticket)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	h.setSessionCookie(c, sessionToken)
	h.setCSRFCookie(c, csrfToken)

	c.Redirect(http.StatusFound, next)
}

// Logout destroys the local session and redirects through CAS logout.
func (h *CASAuthHandler) Logout(c *gin.Context) {
	next := h.validNext(c.Query("next"))

	token, err := c.Cookie(h.cfg.CookieName)
	if err == nil && token != "" {
		_ = h.cfg.SessionService.Delete(token)
	}

	h.clearSessionCookie(c)
	h.clearCSRFCookie(c)

	returnURL := h.cfg.BaseURL + next
	if h.cfg.Client == nil {
		c.Redirect(http.StatusFound, next)
		return
	}
	c.Redirect(http.StatusFound, h.cfg.Client.LogoutURL(returnURL))
}

func (h *CASAuthHandler) validNext(next string) string {
	if next == "" {
		return h.cfg.DefaultNext
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return h.cfg.DefaultNext
	}
	return next
}

func (h *CASAuthHandler) serviceURL(next string) string {
	return fmt.Sprintf("%s/login?next=%s", h.cfg.BaseURL, url.QueryEscape(next))
}

func (h *CASAuthHandler) setSessionCookie(c *gin.Context, token string) {
	h.setCookie(c, h.cfg.CookieName, token, int(h.cfg.SessionTTL.Seconds()), true)
}

func (h *CASAuthHandler) setCSRFCookie(c *gin.Context, token string) {
	h.setCookie(c, h.cfg.CSRFCookieName, token, int(h.cfg.SessionTTL.Seconds()), false)
}

func (h *CASAuthHandler) clearSessionCookie(c *gin.Context) {
	h.setCookie(c, h.cfg.CookieName, "", -1, true)
}

func (h *CASAuthHandler) clearCSRFCookie(c *gin.Context) {
	h.setCookie(c, h.cfg.CSRFCookieName, "", -1, false)
}

func (h *CASAuthHandler) setCookie(c *gin.Context, name, value string, maxAge int, httpOnly bool) {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: httpOnly,
		Secure:   h.cfg.CookieSecure,
		SameSite: h.cfg.CookieSameSite,
	}
	if maxAge < 0 {
		cookie.MaxAge = -1
	}
	c.Writer.Header().Add("Set-Cookie", cookie.String())
}
