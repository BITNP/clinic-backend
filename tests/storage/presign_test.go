package storage_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/storage"
)

func newPresignTestClient(t *testing.T, endpoint, internalEndpoint string) *storage.Client {
	t.Helper()

	client, err := storage.NewClient(storage.Config{
		Endpoint:         endpoint,
		InternalEndpoint: internalEndpoint,
		Bucket:           "test-bucket",
		Region:           "us-east-1",
		AccessKeyID:      "test-access-key",
		SecretAccessKey:  "test-secret-key",
	})
	if err != nil {
		t.Fatalf("storage.NewClient() error = %v", err)
	}
	return client
}

func TestClient_PresignPut(t *testing.T) {
	client := newPresignTestClient(t, "https://s3.example.com", "")
	before := time.Now().UTC()

	request, err := client.PresignPut(context.Background(), "photos/key.jpg", "image/jpeg", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut() error = %v", err)
	}
	after := time.Now().UTC()

	if request.Method != http.MethodPut {
		t.Errorf("method = %q, want %q", request.Method, http.MethodPut)
	}
	if request.URL == "" {
		t.Fatal("URL is empty")
	}
	if request.ExpiresAt.Before(before.Add(5*time.Minute)) || request.ExpiresAt.After(after.Add(5*time.Minute)) {
		t.Errorf("ExpiresAt = %v, want between %v and %v", request.ExpiresAt, before.Add(5*time.Minute), after.Add(5*time.Minute))
	}
	if got := request.RequiredHeaders.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("required Content-Type = %q, want %q", got, "image/jpeg")
	}

	parsed, err := url.Parse(request.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if parsed.Scheme != "https" {
		t.Errorf("scheme = %q, want https", parsed.Scheme)
	}
	if parsed.Host != "s3.example.com" {
		t.Errorf("host = %q, want s3.example.com", parsed.Host)
	}
	if parsed.Path != "/test-bucket/photos/key.jpg" {
		t.Errorf("path = %q, want /test-bucket/photos/key.jpg", parsed.Path)
	}

	query := parsed.Query()
	if query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
		t.Errorf("X-Amz-Algorithm = %q", query.Get("X-Amz-Algorithm"))
	}
	if query.Get("X-Amz-Expires") != "300" {
		t.Errorf("X-Amz-Expires = %q, want 300", query.Get("X-Amz-Expires"))
	}
	if query.Get("X-Amz-Signature") == "" {
		t.Error("X-Amz-Signature is empty")
	}
	if !strings.Contains(query.Get("X-Amz-SignedHeaders"), "content-type") {
		t.Errorf("X-Amz-SignedHeaders = %q, want content-type", query.Get("X-Amz-SignedHeaders"))
	}
}

func TestClient_PresignGet(t *testing.T) {
	client := newPresignTestClient(t, "https://s3.example.com", "")
	before := time.Now().UTC()

	request, err := client.PresignGet(context.Background(), "photos/key.jpg", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	after := time.Now().UTC()

	if request.Method != http.MethodGet {
		t.Errorf("method = %q, want %q", request.Method, http.MethodGet)
	}
	if request.URL == "" {
		t.Fatal("URL is empty")
	}
	if request.ExpiresAt.Before(before.Add(10*time.Minute)) || request.ExpiresAt.After(after.Add(10*time.Minute)) {
		t.Errorf("ExpiresAt = %v, want between %v and %v", request.ExpiresAt, before.Add(10*time.Minute), after.Add(10*time.Minute))
	}

	parsed, err := url.Parse(request.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if parsed.Host != "s3.example.com" {
		t.Errorf("host = %q, want s3.example.com", parsed.Host)
	}
	if parsed.Path != "/test-bucket/photos/key.jpg" {
		t.Errorf("path = %q, want /test-bucket/photos/key.jpg", parsed.Path)
	}
	if parsed.Query().Get("X-Amz-Expires") != "600" {
		t.Errorf("X-Amz-Expires = %q, want 600", parsed.Query().Get("X-Amz-Expires"))
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Error("X-Amz-Signature is empty")
	}
}

func TestClient_PresignPut_RequiresContentType(t *testing.T) {
	client := newPresignTestClient(t, "https://s3.example.com", "")

	for _, contentType := range []string{"", "   "} {
		_, err := client.PresignPut(context.Background(), "photos/key.jpg", contentType, 5*time.Minute)
		if err == nil {
			t.Fatalf("PresignPut() with content type %q returned nil error", contentType)
		}
		if !strings.Contains(err.Error(), "content type is required") {
			t.Fatalf("error = %q, want content type error", err)
		}
	}
}

func TestClient_PresignRejectsEmptyKey(t *testing.T) {
	client := newPresignTestClient(t, "https://s3.example.com", "")

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "put",
			call: func() error {
				_, err := client.PresignPut(context.Background(), "", "image/jpeg", 5*time.Minute)
				return err
			},
		},
		{
			name: "get",
			call: func() error {
				_, err := client.PresignGet(context.Background(), "", 5*time.Minute)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "object key is required") {
				t.Fatalf("error = %q, want object key error", err)
			}
		})
	}
}

func TestClient_PresignRejectsInvalidExpiry(t *testing.T) {
	client := newPresignTestClient(t, "https://s3.example.com", "")

	tests := []struct {
		name   string
		expiry time.Duration
		call   func(time.Duration) error
	}{
		{
			name:   "put zero",
			expiry: 0,
			call: func(expiry time.Duration) error {
				_, err := client.PresignPut(context.Background(), "photos/key.jpg", "image/jpeg", expiry)
				return err
			},
		},
		{
			name:   "put negative",
			expiry: -time.Second,
			call: func(expiry time.Duration) error {
				_, err := client.PresignPut(context.Background(), "photos/key.jpg", "image/jpeg", expiry)
				return err
			},
		},
		{
			name:   "get zero",
			expiry: 0,
			call: func(expiry time.Duration) error {
				_, err := client.PresignGet(context.Background(), "photos/key.jpg", expiry)
				return err
			},
		},
		{
			name:   "get negative",
			expiry: -time.Second,
			call: func(expiry time.Duration) error {
				_, err := client.PresignGet(context.Background(), "photos/key.jpg", expiry)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(tt.expiry)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "expiry must be positive") {
				t.Fatalf("error = %q, want expiry error", err)
			}
		})
	}
}

func TestClient_PresignUsesPublicEndpoint(t *testing.T) {
	client := newPresignTestClient(t, "https://public.example.com", "http://internal.example.com:9000")

	tests := []struct {
		name string
		call func() (storage.SignedRequest, error)
	}{
		{
			name: "put",
			call: func() (storage.SignedRequest, error) {
				return client.PresignPut(context.Background(), "photos/key.jpg", "image/jpeg", 5*time.Minute)
			},
		},
		{
			name: "get",
			call: func() (storage.SignedRequest, error) {
				return client.PresignGet(context.Background(), "photos/key.jpg", 5*time.Minute)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := tt.call()
			if err != nil {
				t.Fatalf("presign error = %v", err)
			}

			parsed, err := url.Parse(request.URL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if parsed.Host != "public.example.com" {
				t.Errorf("host = %q, want public.example.com", parsed.Host)
			}
			if strings.Contains(request.URL, "internal.example.com") {
				t.Errorf("URL used the internal endpoint")
			}
		})
	}
}

func TestClient_Presign_DoesNotContactStorage(t *testing.T) {
	client := newPresignTestClient(t, "http://127.0.0.1:1", "")

	if _, err := client.PresignPut(context.Background(), "photos/key.jpg", "image/jpeg", 5*time.Minute); err != nil {
		t.Fatalf("PresignPut() error = %v", err)
	}
	if _, err := client.PresignGet(context.Background(), "photos/key.jpg", 5*time.Minute); err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
}
