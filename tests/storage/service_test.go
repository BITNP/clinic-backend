package storage_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/storage"
)

func TestNewService_Disabled(t *testing.T) {
	service, err := storage.NewService(nil, 5*time.Minute, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if service.Enabled() {
		t.Fatal("Enabled() = true, want false")
	}

	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "put",
			call: func() error {
				_, err := service.Put(ctx, "photos/key.jpg", strings.NewReader("body"), 4, "image/jpeg")
				return err
			},
		},
		{
			name: "get",
			call: func() error {
				_, err := service.Get(ctx, "photos/key.jpg")
				return err
			},
		},
		{
			name: "head",
			call: func() error {
				_, err := service.Head(ctx, "photos/key.jpg")
				return err
			},
		},
		{
			name: "delete",
			call: func() error {
				return service.Delete(ctx, "photos/key.jpg")
			},
		},
		{
			name: "presign put",
			call: func() error {
				_, err := service.PresignPut(ctx, "photos/key.jpg", "image/jpeg")
				return err
			},
		},
		{
			name: "presign get",
			call: func() error {
				_, err := service.PresignGet(ctx, "photos/key.jpg")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, storage.ErrStorageDisabled) {
				t.Fatalf("error = %v, want ErrStorageDisabled", err)
			}
		})
	}
}

func TestNewService_InvalidExpiry(t *testing.T) {
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
			service, err := storage.NewService(nil, tt.putTTL, tt.getTTL)
			if err == nil {
				t.Fatal("expected an error")
			}
			if service != nil {
				t.Fatal("expected a nil service")
			}
		})
	}
}

func TestService_PresignDefaults(t *testing.T) {
	client := newPresignTestClient(t, "https://s3.example.com", "")
	service, err := storage.NewService(client, 2*time.Minute, 3*time.Minute)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if !service.Enabled() {
		t.Fatal("Enabled() = false, want true")
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
