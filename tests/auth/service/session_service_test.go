package service_test

import (
	"testing"
	"time"

	authmodel "clinic-backend/internal/auth/model"
	authservice "clinic-backend/internal/auth/service"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupSessionTestDB(t *testing.T) (*authservice.SessionService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&authmodel.AuthSession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return authservice.NewSessionService(db, time.Hour), db
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
	svc := authservice.NewSessionService(db, -time.Hour)

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
	svc := authservice.NewSessionService(db, -time.Hour)

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
