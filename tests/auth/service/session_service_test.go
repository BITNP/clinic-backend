package service_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	authmodel "clinic-backend/internal/auth/model"
	authservice "clinic-backend/internal/auth/service"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func sessionSQLDB(t *testing.T, db *gorm.DB) *sql.DB {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

func setupSessionTestDB(t *testing.T) (*authservice.SessionService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&authmodel.AuthSession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return authservice.NewSessionService(sessionSQLDB(t, db), time.Hour), db
}

func TestSessionService_CreateAndGet(t *testing.T) {
	svc, _ := setupSessionTestDB(t)

	token, csrf, err := svc.Create(42, "staff", "ST-123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if token == "" || csrf == "" {
		t.Fatal("expected non-empty tokens")
	}

	sess, err := svc.Get(token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.StaffID != 42 {
		t.Errorf("staff id: got %d", sess.StaffID)
	}
	if sess.Role != "staff" {
		t.Errorf("role: got %q", sess.Role)
	}
	if sess.CASTicket != "ST-123" {
		t.Errorf("cas ticket: got %q", sess.CASTicket)
	}
}

func TestSessionService_GetExpired(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&authmodel.AuthSession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := authservice.NewSessionService(sessionSQLDB(t, db), -time.Hour)

	token, _, err := svc.Create(42, "staff", "ST-123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Get(token)
	if err == nil {
		t.Fatal("expected error for expired session")
	}
}

func TestSessionService_Delete(t *testing.T) {
	svc, _ := setupSessionTestDB(t)

	token, _, err := svc.Create(42, "staff", "ST-123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(token); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = svc.Get(token)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSessionService_ValidateCSRF(t *testing.T) {
	svc, _ := setupSessionTestDB(t)

	token, csrf, err := svc.Create(42, "staff", "ST-123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !svc.ValidateCSRF(token, csrf) {
		t.Error("expected valid csrf")
	}
	if svc.ValidateCSRF(token, "wrong") {
		t.Error("expected invalid csrf")
	}
	if svc.ValidateCSRF("wrong", csrf) {
		t.Error("expected invalid session")
	}
}

func TestSessionService_DeleteExpired(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&authmodel.AuthSession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := authservice.NewSessionService(sessionSQLDB(t, db), -time.Hour)

	token, _, err := svc.Create(42, "staff", "ST-123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := svc.DeleteExpired(); err != nil {
		t.Fatalf("delete expired: %v", err)
	}

	_, err = svc.Get(token)
	if err == nil {
		t.Fatal("expected error after deleting expired sessions")
	}
}

func TestSessionService_StorageAndExpiration(t *testing.T) {
	svc, db := setupSessionTestDB(t)
	active, csrf, err := svc.Create(42, "staff", "ST-active")
	if err != nil {
		t.Fatalf("create active: %v", err)
	}
	expiredSvc := authservice.NewSessionService(sessionSQLDB(t, db), -time.Hour)
	expired, _, err := expiredSvc.Create(43, "admin", "ST-expired")
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	var rows []authmodel.AuthSession
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load sessions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two sessions, got %d", len(rows))
	}
	for _, row := range rows {
		if row.TokenHash == active || row.CSRFTokenHash == csrf || row.CreatedAt.IsZero() {
			t.Fatalf("tokens must be hashed and creation time recorded: %+v", row)
		}
	}
	if _, err := svc.Get(expired); !errors.Is(err, authservice.ErrSessionNotFound) {
		t.Fatalf("expired lookup: %v", err)
	}
	if err := svc.DeleteExpired(); err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if _, err := svc.Get(active); err != nil {
		t.Fatalf("active session removed: %v", err)
	}
	var count int64
	if err := db.Model(&authmodel.AuthSession{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("expected one stored session, got %d: %v", count, err)
	}
	if err := svc.Delete("missing"); err != nil {
		t.Fatalf("delete missing must succeed: %v", err)
	}
}

func TestSessionService_LegacyNullFieldsAndDatabaseFailure(t *testing.T) {
	svc, db := setupSessionTestDB(t)
	token, _, err := svc.Create(42, "staff", "ST-ticket")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.Model(&authmodel.AuthSession{}).Where("staff_id = ?", 42).
		Updates(map[string]any{"cas_ticket": nil, "created_at": nil}).Error; err != nil {
		t.Fatalf("set null fields: %v", err)
	}
	sess, err := svc.Get(token)
	if err != nil || sess.CASTicket != "" || !sess.CreatedAt.IsZero() {
		t.Fatalf("load nullable fields: %+v, %v", sess, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if _, err := svc.Get(token); err == nil || errors.Is(err, authservice.ErrSessionNotFound) {
		t.Fatalf("lookup database error: %v", err)
	}
	if _, _, err := svc.Create(42, "staff", ""); err == nil {
		t.Fatal("expected create database error")
	}
	if err := svc.Delete(token); err == nil {
		t.Fatal("expected delete database error")
	}
	if err := svc.DeleteExpired(); err == nil {
		t.Fatal("expected cleanup database error")
	}
	if svc.ValidateCSRF(token, "anything") {
		t.Fatal("CSRF validation must fail on database error")
	}
}
