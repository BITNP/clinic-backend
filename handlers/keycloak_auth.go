package handlers

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"clinic-backend/models"
	"clinic-backend/services"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// KeycloakAuthenticator validates Keycloak Bearer JWTs and resolves them to staff.
type KeycloakAuthenticator struct {
	realmURL string
	clientID string
	staffSvc *services.StaffService

	staffVersion int

	mu     sync.RWMutex
	cached *cachedJWKS
}

// NewKeycloakAuthenticator creates an authenticator that validates Keycloak JWTs.
func NewKeycloakAuthenticator(realmURL, clientID string, staffSvc *services.StaffService) *KeycloakAuthenticator {
	return &KeycloakAuthenticator{
		realmURL: strings.TrimRight(realmURL, "/"),
		clientID: clientID,
		staffSvc: staffSvc,
	}
}

// SetStaffVersion sets the currently required login version stamped onto staff.
func (a *KeycloakAuthenticator) SetStaffVersion(version int) {
	a.staffVersion = version
}

// Configured reports whether realm URL and client ID are both set.
func (a *KeycloakAuthenticator) Configured() bool {
	return a.realmURL != "" && a.clientID != ""
}

// Authenticate validates a raw JWT and returns the resolved staff and role.
func (a *KeycloakAuthenticator) Authenticate(tokenStr string) (models.ClinicStaff, StaffRole, error) {
	token, err := a.parseJWT(tokenStr)
	if err != nil {
		return models.ClinicStaff{}, "", fmt.Errorf("parse jwt: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return models.ClinicStaff{}, "", errors.New("invalid token claims")
	}

	groups := extractGroups(claims)
	role := determineRole(groups)
	if role == "" {
		return models.ClinicStaff{}, "", errors.New("insufficient group membership")
	}

	accountID := extractAccountID(claims)
	if accountID == "" {
		return models.ClinicStaff{}, "", errors.New("token missing user identifier")
	}

	realname := extractRealname(claims)
	email := extractEmail(claims)
	staff, err := a.staffSvc.GetOrCreateByAccountID(accountID, realname, email)
	if err != nil {
		return models.ClinicStaff{}, "", fmt.Errorf("resolve staff: %w", err)
	}

	if err := a.staffSvc.UpdateRole(staff.ID, string(role)); err != nil {
		log.Printf("warning: failed to persist role %s for staff %d: %v", role, staff.ID, err)
	}

	if err := a.staffSvc.EnsureWorkYears(staff.ID, extractWorkYears(groups)); err != nil {
		log.Printf("warning: failed to record work years for staff %d: %v", staff.ID, err)
	}

	if err := a.staffSvc.UpdateVersion(staff.ID, a.staffVersion); err != nil {
		log.Printf("warning: failed to update version for staff %d: %v", staff.ID, err)
	}

	return staff, role, nil
}

func extractGroups(claims jwt.MapClaims) []string {
	switch v := claims["groups"].(type) {
	case []any:
		groups := make([]string, 0, len(v))
		for _, g := range v {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
		return groups
	case []string:
		return v
	}
	return nil
}

func extractAccountID(claims jwt.MapClaims) string {
	if v, ok := claims["preferred_username"].(string); ok && v != "" {
		return v
	}
	if v, ok := claims["sub"].(string); ok && v != "" {
		return v
	}
	return ""
}

func extractRealname(claims jwt.MapClaims) string {
	if v, ok := claims["name"].(string); ok && v != "" {
		return v
	}
	return ""
}

func extractEmail(claims jwt.MapClaims) string {
	if v, ok := claims["email"].(string); ok && v != "" {
		return v
	}
	return ""
}

func (a *KeycloakAuthenticator) parseJWT(tokenStr string) (*jwt.Token, error) {
	return jwt.Parse(tokenStr, a.keyFunc,
		jwt.WithIssuer(a.realmURL+"/"),
		jwt.WithAudience(a.clientID),
		jwt.WithValidMethods([]string{"RS256"}),
	)
}

func (a *KeycloakAuthenticator) keyFunc(token *jwt.Token) (any, error) {
	kid, ok := token.Header["kid"].(string)
	if !ok {
		return nil, errors.New("token missing kid header")
	}

	keys, err := a.getKeys()
	if err != nil {
		return nil, fmt.Errorf("get jwks: %w", err)
	}

	pub, ok := keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown kid %s", kid)
	}
	return pub, nil
}

func (a *KeycloakAuthenticator) getKeys() (map[string]*rsa.PublicKey, error) {
	a.mu.RLock()
	c := a.cached
	a.mu.RUnlock()

	if c != nil && time.Since(c.fetched) < c.ttl {
		return c.keys, nil
	}

	return a.fetchKeys()
}

func (a *KeycloakAuthenticator) fetchKeys() (map[string]*rsa.PublicKey, error) {
	url := a.realmURL + "/protocol/openid-connect/certs"
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}

	var set jwksSet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := k.rsaPublicKey()
		if err != nil {
			return nil, fmt.Errorf("parse jwk kid=%s: %w", k.Kid, err)
		}
		keys[k.Kid] = pub
	}

	a.mu.Lock()
	a.cached = &cachedJWKS{
		keys:    keys,
		fetched: time.Now(),
		ttl:     1 * time.Hour,
	}
	a.mu.Unlock()

	return keys, nil
}

// jwksKey is a single JWK entry for an RSA public key.
type jwksKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
}

// jwksSet is the JWKS response body.
type jwksSet struct {
	Keys []jwksKey `json:"keys"`
}

// cachedJWKS holds a parsed JWKS with its fetch time.
type cachedJWKS struct {
	keys    map[string]*rsa.PublicKey
	fetched time.Time
	ttl     time.Duration
}

func (k jwksKey) rsaPublicKey() (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	if len(eBytes) < 8 {
		pad := make([]byte, 8-len(eBytes))
		eBytes = append(pad, eBytes...)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Uint64())

	return &rsa.PublicKey{N: n, E: e}, nil
}

// NewKeycloakAuthMiddleware creates a Gin middleware that validates Bearer JWTs
// from Keycloak and populates the staff context. If realmURL or clientID is empty
// the middleware rejects all requests.
func NewKeycloakAuthMiddleware(realmURL, clientID string, staffSvc *services.StaffService) gin.HandlerFunc {
	a := NewKeycloakAuthenticator(realmURL, clientID, staffSvc)
	return func(c *gin.Context) {
		if !a.Configured() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "bearer authentication not configured"})
			return
		}
		tokenStr, err := extractBearer(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed authorization header"})
			return
		}
		staff, role, err := a.Authenticate(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(staffContextKey, staff)
		c.Set(staffRoleContextKey, role)
		c.Next()
	}
}

func extractBearer(c *gin.Context) (string, error) {
	auth := c.GetHeader("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", errors.New("no bearer token")
	}
	return strings.TrimPrefix(auth, "Bearer "), nil
}
