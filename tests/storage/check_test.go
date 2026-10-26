package storage_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/storage"
)

func TestClient_Check(t *testing.T) {
	requests := make(chan struct {
		method string
		path   string
	}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct {
			method string
			path   string
		}{method: r.Method, path: r.URL.Path}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newPresignTestClient(t, server.URL, "")
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	got := <-requests
	if got.method != http.MethodHead {
		t.Errorf("method = %q, want %q", got.method, http.MethodHead)
	}
	if got.path != "/test-bucket" {
		t.Errorf("path = %q, want /test-bucket", got.path)
	}
}

func TestClient_Check_UsesBackendEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newPresignTestClient(t, "http://127.0.0.1:1", server.URL)
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestClient_Check_Errors(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "missing bucket", status: http.StatusNotFound},
		{name: "denied", status: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			client := newPresignTestClient(t, server.URL, "")
			err := client.Check(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "test-bucket") {
				t.Fatalf("error = %q, want bucket name", err)
			}
		})
	}
}

func TestClient_Check_Unreachable(t *testing.T) {
	client := newPresignTestClient(t, "http://127.0.0.1:1", "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Check(ctx); err == nil {
		t.Fatal("expected an error")
	}
}

func TestClient_Check_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	client := newPresignTestClient(t, server.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := client.Check(ctx)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Check() took %v, want less than 1s", elapsed)
	}
}

func TestClient_Check_DoesNotExposeSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	const secret = "super-secret-value"
	client, err := storage.NewClient(storage.Config{
		Endpoint:        server.URL,
		Bucket:          "test-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: secret,
	})
	if err != nil {
		t.Fatalf("storage.NewClient() error = %v", err)
	}

	err = client.Check(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed the secret access key: %q", err)
	}
}
