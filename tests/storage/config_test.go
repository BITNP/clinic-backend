package storage_test

import (
	"strings"
	"testing"

	"clinic-backend/internal/storage"
)

func validConfig() storage.Config {
	return storage.Config{
		Endpoint:        "http://localhost:9000",
		Bucket:          "clinic-photos-dev",
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	}
}

func TestNewClient_RequiresFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*storage.Config)
		want   string
	}{
		{
			name:   "endpoint",
			mutate: func(cfg *storage.Config) { cfg.Endpoint = "" },
			want:   "endpoint is required",
		},
		{
			name:   "bucket",
			mutate: func(cfg *storage.Config) { cfg.Bucket = "" },
			want:   "bucket is required",
		},
		{
			name:   "region",
			mutate: func(cfg *storage.Config) { cfg.Region = "" },
			want:   "region is required",
		},
		{
			name:   "access key ID",
			mutate: func(cfg *storage.Config) { cfg.AccessKeyID = "" },
			want:   "access key ID is required",
		},
		{
			name:   "secret access key",
			mutate: func(cfg *storage.Config) { cfg.SecretAccessKey = "" },
			want:   "secret access key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			client, err := storage.NewClient(cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if client != nil {
				t.Fatal("expected a nil client")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %q", tt.want, err)
			}
		})
	}
}

func TestNewClient_RejectsInvalidEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name:     "malformed URL",
			endpoint: "http://[::1",
			want:     "not a valid URL",
		},
		{
			name:     "missing scheme",
			endpoint: "localhost:9000",
			want:     "must use http or https",
		},
		{
			name:     "unsupported scheme",
			endpoint: "ftp://localhost:9000",
			want:     "must use http or https",
		},
		{
			name:     "missing hostname",
			endpoint: "http://",
			want:     "must include a hostname",
		},
		{
			name:     "missing hostname with path",
			endpoint: "http:///bucket",
			want:     "must include a hostname",
		},
		{
			name:     "embedded credentials",
			endpoint: "http://user:password@localhost:9000",
			want:     "must not contain credentials",
		},
		{
			name:     "query string",
			endpoint: "http://localhost:9000?token=secret",
			want:     "must not contain a query string",
		},
		{
			name:     "empty query string",
			endpoint: "http://localhost:9000?",
			want:     "must not contain a query string",
		},
		{
			name:     "fragment",
			endpoint: "http://localhost:9000#section",
			want:     "must not contain a fragment",
		},
		{
			name:     "empty fragment",
			endpoint: "http://localhost:9000#",
			want:     "must not contain a fragment",
		},
		{
			name:     "path",
			endpoint: "http://localhost:9000/api",
			want:     "must not contain a path",
		},
		{
			name:     "double slash path",
			endpoint: "http://localhost:9000//",
			want:     "must not contain a path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Endpoint = tt.endpoint

			client, err := storage.NewClient(cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if client != nil {
				t.Fatal("expected a nil client")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %q", tt.want, err)
			}
		})
	}
}

func TestNewClient_RejectsInvalidInternalEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		internal string
		want     string
	}{
		{
			name:     "missing scheme",
			internal: "rustfs:9000",
			want:     "internal endpoint must use http or https",
		},
		{
			name:     "missing hostname",
			internal: "http://",
			want:     "internal endpoint must include a hostname",
		},
		{
			name:     "embedded credentials",
			internal: "http://user:password@rustfs:9000",
			want:     "internal endpoint must not contain credentials",
		},
		{
			name:     "path",
			internal: "http://rustfs:9000/api",
			want:     "internal endpoint must not contain a path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.InternalEndpoint = tt.internal

			client, err := storage.NewClient(cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if client != nil {
				t.Fatal("expected a nil client")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %q", tt.want, err)
			}
		})
	}
}

func TestNewClient_DoesNotExposeSecrets(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoint = ""
	cfg.SecretAccessKey = "super-secret-value"

	_, err := storage.NewClient(cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), cfg.SecretAccessKey) {
		t.Fatalf("error exposed the secret access key: %q", err)
	}
}
