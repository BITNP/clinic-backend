package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func envMap(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func validStorageEnv() map[string]string {
	return map[string]string{
		"S3_ENDPOINT":          "http://127.0.0.1:1",
		"S3_INTERNAL_ENDPOINT": "http://rustfs:9000",
		"S3_BUCKET":            "clinic-photos-dev",
		"S3_REGION":            "us-east-1",
		"S3_ACCESS_KEY_ID":     "test-access-key",
		"S3_SECRET_ACCESS_KEY": "test-secret-key",
		"S3_PUT_URL_TTL":       "2m",
		"S3_GET_URL_TTL":       "3m",
	}
}

func TestLoadStorageService_Valid(t *testing.T) {
	service, err := loadStorageService(envMap(validStorageEnv()))
	if err != nil {
		t.Fatalf("loadStorageService() error = %v", err)
	}
	if service == nil {
		t.Fatal("loadStorageService() returned a nil service")
	}

	put, err := service.PresignPut(context.Background(), "photos/key.jpg", "image/jpeg")
	if err != nil {
		t.Fatalf("PresignPut() error = %v", err)
	}
	assertQueryExpiry(t, put.URL, "120")

	get, err := service.PresignGet(context.Background(), "photos/key.jpg")
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	assertQueryExpiry(t, get.URL, "180")
}

func TestLoadStorageService_DefaultExpiry(t *testing.T) {
	values := validStorageEnv()
	delete(values, "S3_PUT_URL_TTL")
	delete(values, "S3_GET_URL_TTL")

	service, err := loadStorageService(envMap(values))
	if err != nil {
		t.Fatalf("loadStorageService() error = %v", err)
	}

	put, err := service.PresignPut(context.Background(), "photos/key.jpg", "image/jpeg")
	if err != nil {
		t.Fatalf("PresignPut() error = %v", err)
	}
	assertQueryExpiry(t, put.URL, "300")

	get, err := service.PresignGet(context.Background(), "photos/key.jpg")
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	assertQueryExpiry(t, get.URL, "600")
}

func TestLoadStorageService_EmptyConfiguration(t *testing.T) {
	service, err := loadStorageService(envMap(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	if service != nil {
		t.Fatal("expected a nil service")
	}
	if !strings.Contains(err.Error(), "endpoint is required") {
		t.Fatalf("error = %q, want endpoint error", err)
	}
}

func TestLoadStorageService_MissingRequiredSettings(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "endpoint", key: "S3_ENDPOINT", want: "endpoint is required"},
		{name: "bucket", key: "S3_BUCKET", want: "bucket is required"},
		{name: "region", key: "S3_REGION", want: "region is required"},
		{name: "access key", key: "S3_ACCESS_KEY_ID", want: "access key ID is required"},
		{name: "secret key", key: "S3_SECRET_ACCESS_KEY", want: "secret access key is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validStorageEnv()
			delete(values, tt.key)

			service, err := loadStorageService(envMap(values))
			if err == nil {
				t.Fatal("expected an error")
			}
			if service != nil {
				t.Fatal("expected a nil service")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadStorageService_InvalidSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{
			name:   "endpoint",
			mutate: func(values map[string]string) { values["S3_ENDPOINT"] = "localhost:9000" },
			want:   "endpoint must use http or https",
		},
		{
			name:   "internal endpoint",
			mutate: func(values map[string]string) { values["S3_INTERNAL_ENDPOINT"] = "rustfs:9000" },
			want:   "internal endpoint must use http or https",
		},
		{
			name:   "put expiry syntax",
			mutate: func(values map[string]string) { values["S3_PUT_URL_TTL"] = "not-a-duration" },
			want:   "invalid S3_PUT_URL_TTL",
		},
		{
			name:   "put expiry zero",
			mutate: func(values map[string]string) { values["S3_PUT_URL_TTL"] = "0s" },
			want:   "S3_PUT_URL_TTL must be positive",
		},
		{
			name:   "get expiry negative",
			mutate: func(values map[string]string) { values["S3_GET_URL_TTL"] = "-1m" },
			want:   "S3_GET_URL_TTL must be positive",
		},
		{
			name:   "get expiry syntax",
			mutate: func(values map[string]string) { values["S3_GET_URL_TTL"] = "later" },
			want:   "invalid S3_GET_URL_TTL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validStorageEnv()
			tt.mutate(values)

			service, err := loadStorageService(envMap(values))
			if err == nil {
				t.Fatal("expected an error")
			}
			if service != nil {
				t.Fatal("expected a nil service")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadStorageService_ConfigLoadingDoesNotContactStorage(t *testing.T) {
	service, err := loadStorageService(envMap(validStorageEnv()))
	if err != nil {
		t.Fatalf("loadStorageService() error = %v", err)
	}
	if service == nil {
		t.Fatal("loadStorageService() returned a nil service")
	}

	// Signing is offline, so this must work even though nothing listens on the
	// configured endpoint. Bucket access is checked separately during startup.
	if _, err := service.PresignGet(context.Background(), "photos/key.jpg"); err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
}

func TestLoadStorageService_DoesNotExposeSecrets(t *testing.T) {
	values := validStorageEnv()
	delete(values, "S3_ENDPOINT")
	values["S3_SECRET_ACCESS_KEY"] = "super-secret-value"

	_, err := loadStorageService(envMap(values))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), values["S3_SECRET_ACCESS_KEY"]) {
		t.Fatalf("error exposed the secret access key: %q", err)
	}
}

func assertQueryExpiry(t *testing.T, rawURL, want string) {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if got := parsed.Query().Get("X-Amz-Expires"); got != want {
		t.Fatalf("X-Amz-Expires = %q, want %q", got, want)
	}
}
