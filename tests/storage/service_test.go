package storage_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"clinic-backend/internal/storage"
)

func TestNewService_RequiresClient(t *testing.T) {
	service, err := storage.NewService(nil, 5*time.Minute, 10*time.Minute)
	if err == nil {
		t.Fatal("expected an error")
	}
	if service != nil {
		t.Fatal("expected a nil service")
	}
}

func TestNewService_InvalidExpiry(t *testing.T) {
	client := newPresignTestClient(t, "https://s3.example.com", "")

	tests := []struct {
		name   string
		putTTL time.Duration
		getTTL time.Duration
	}{
		{name: "zero put", putTTL: 0, getTTL: 10 * time.Minute},
		{name: "negative put", putTTL: -time.Minute, getTTL: 10 * time.Minute},
		{name: "zero get", putTTL: 5 * time.Minute, getTTL: 0},
		{name: "negative get", putTTL: 5 * time.Minute, getTTL: -time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := storage.NewService(client, tt.putTTL, tt.getTTL)
			if err == nil {
				t.Fatal("expected an error")
			}
			if service != nil {
				t.Fatal("expected a nil service")
			}
		})
	}
}

func TestService_Check(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %q, want %q", r.Method, http.MethodHead)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newPresignTestClient(t, server.URL, "")
	service, err := storage.NewService(client, 5*time.Minute, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if err := service.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestService_PresignDefaults(t *testing.T) {
	client := newPresignTestClient(t, "https://s3.example.com", "")
	service, err := storage.NewService(client, 2*time.Minute, 3*time.Minute)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	put, err := service.PresignPut(context.Background(), "photos/key.jpg", "image/jpeg")
	if err != nil {
		t.Fatalf("PresignPut() error = %v", err)
	}
	if put.Method != http.MethodPut {
		t.Errorf("method = %q, want %q", put.Method, http.MethodPut)
	}
	if got := put.RequiredHeaders.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", got)
	}
	assertPresignExpiry(t, put.URL, "120")

	get, err := service.PresignGet(context.Background(), "photos/key.jpg")
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	if get.Method != http.MethodGet {
		t.Errorf("method = %q, want %q", get.Method, http.MethodGet)
	}
	assertPresignExpiry(t, get.URL, "180")
}

func assertPresignExpiry(t *testing.T, rawURL, want string) {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if got := parsed.Query().Get("X-Amz-Expires"); got != want {
		t.Fatalf("X-Amz-Expires = %q, want %q", got, want)
	}
}
