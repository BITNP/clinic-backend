package service

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	authmodel "clinic-backend/internal/auth/model"
)

// ErrSessionNotFound is returned when a session token does not exist or has expired.
var ErrSessionNotFound = errors.New("session not found")

// SessionService manages opaque server-side sessions.
type SessionService struct {
	db  *sql.DB
	ttl time.Duration
}

// NewSessionService creates a session service with the given database and TTL.
func NewSessionService(db *sql.DB, ttl time.Duration) *SessionService {
	return &SessionService{db: db, ttl: ttl}
}

// Create generates a new session for a staff member and returns the opaque
// session token and the corresponding CSRF token.
func (s *SessionService) Create(staffID int, role, casTicket string) (sessionToken, csrfToken string, err error) {
	sessionToken, err = generateToken()
	if err != nil {
		return "", "", err
	}
	csrfToken, err = generateToken()
	if err != nil {
		return "", "", err
	}

	now := time.Now()
	_, err = s.db.Exec(`INSERT INTO auth_session
		(token_hash, staff_id, role, csrf_token_hash, cas_ticket, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		hashToken(sessionToken), staffID, role, hashToken(csrfToken), casTicket, now.Add(s.ttl), now)
	if err != nil {
		return "", "", fmt.Errorf("create session: %w", err)
	}
	return sessionToken, csrfToken, nil
}

// Get resolves a session token to its active session record.
func (s *SessionService) Get(token string) (authmodel.AuthSession, error) {
	var sess authmodel.AuthSession
	var casTicket sql.NullString
	var createdAt sql.NullTime
	err := s.db.QueryRow(`SELECT id, token_hash, staff_id, role, csrf_token_hash, cas_ticket, expires_at, created_at
		FROM auth_session WHERE token_hash = $1 AND expires_at > $2`, hashToken(token), time.Now()).
		Scan(&sess.ID, &sess.TokenHash, &sess.StaffID, &sess.Role, &sess.CSRFTokenHash, &casTicket, &sess.ExpiresAt, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authmodel.AuthSession{}, ErrSessionNotFound
		}
		return authmodel.AuthSession{}, fmt.Errorf("lookup session: %w", err)
	}
	sess.CASTicket = casTicket.String
	if createdAt.Valid {
		sess.CreatedAt = createdAt.Time
	}
	return sess, nil
}

// ValidateCSRF checks whether the supplied CSRF token matches the session.
func (s *SessionService) ValidateCSRF(sessionToken, csrfToken string) bool {
	sess, err := s.Get(sessionToken)
	if err != nil {
		return false
	}
	return sess.CSRFTokenHash == hashToken(csrfToken)
}

// Delete removes a session by token.
func (s *SessionService) Delete(token string) error {
	if _, err := s.db.Exec("DELETE FROM auth_session WHERE token_hash = $1", hashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpired removes all sessions past their expiration time.
func (s *SessionService) DeleteExpired() error {
	if _, err := s.db.Exec("DELETE FROM auth_session WHERE expires_at <= $1", time.Now()); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
