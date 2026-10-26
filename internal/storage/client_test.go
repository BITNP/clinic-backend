package storage

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestNewClient_EndpointSelection(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		internal    string
		wantBackend string
		wantPresign string
	}{
		{
			name:        "fallback to public endpoint",
			endpoint:    "https://s3.bitnp.net",
			internal:    "",
			wantBackend: "https://s3.bitnp.net",
			wantPresign: "https://s3.bitnp.net",
		},
		{
			name:        "internal endpoint override",
			endpoint:    "https://s3.bitnp.net",
			internal:    "http://rustfs:9000",
			wantBackend: "http://rustfs:9000",
			wantPresign: "https://s3.bitnp.net",
		},
		{
			name:        "root path is normalized",
			endpoint:    "https://s3.bitnp.net/",
			internal:    "http://rustfs:9000/",
			wantBackend: "http://rustfs:9000",
			wantPresign: "https://s3.bitnp.net",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(Config{
				Endpoint:         tt.endpoint,
				InternalEndpoint: tt.internal,
				Bucket:           "clinic-photos",
				Region:           "us-east-1",
				AccessKeyID:      "test-access-key",
				SecretAccessKey:  "test-secret-key",
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			backend := client.backend.Options()
			presign := client.presign.Options()

			assertEndpoint(t, backend.BaseEndpoint, tt.wantBackend)
			assertEndpoint(t, presign.BaseEndpoint, tt.wantPresign)
		})
	}
}

func TestNewClient_ConfiguresSDKClients(t *testing.T) {
	cfg := Config{
		Endpoint:         "https://s3.bitnp.net",
		InternalEndpoint: "http://rustfs:9000",
		Bucket:           "clinic-photos",
		Region:           "us-east-1",
		AccessKeyID:      "test-access-key",
		SecretAccessKey:  "test-secret-key",
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if client.bucket != cfg.Bucket {
		t.Fatalf("bucket = %q, want %q", client.bucket, cfg.Bucket)
	}

	clients := []struct {
		name string
		opts s3.Options
	}{
		{name: "backend", opts: client.backend.Options()},
		{name: "presign", opts: client.presign.Options()},
	}

	for _, tt := range clients {
		if tt.opts.Region != cfg.Region {
			t.Errorf("%s region = %q, want %q", tt.name, tt.opts.Region, cfg.Region)
		}
		if !tt.opts.UsePathStyle {
			t.Errorf("%s UsePathStyle = false, want true", tt.name)
		}

		creds, err := tt.opts.Credentials.Retrieve(context.Background())
		if err != nil {
			t.Fatalf("%s credentials: %v", tt.name, err)
		}
		if creds.AccessKeyID != cfg.AccessKeyID {
			t.Errorf("%s access key ID = %q, want %q", tt.name, creds.AccessKeyID, cfg.AccessKeyID)
		}
		if creds.SecretAccessKey != cfg.SecretAccessKey {
			t.Errorf("%s secret access key does not match the configured value", tt.name)
		}
	}
}

func assertEndpoint(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Fatal("endpoint is nil")
	}
	if *got != want {
		t.Fatalf("endpoint = %q, want %q", *got, want)
	}
}
