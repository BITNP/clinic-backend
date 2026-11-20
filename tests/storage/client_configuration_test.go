package storage_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/storage"
)

func TestNewClient_EndpointSelection(t *testing.T) {
	tests := []struct {
		name           string
		useInternal    bool
		trailingSlash  bool
		wantBackendHit string
	}{
		{name: "fallback to public endpoint", wantBackendHit: "public"},
		{name: "internal endpoint override", useInternal: true, wantBackendHit: "internal"},
		{name: "root path is normalized", useInternal: true, trailingSlash: true, wantBackendHit: "internal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var publicHits, internalHits int
			publicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				publicHits++
				w.Header().Set("ETag", `"etag"`)
			}))
			defer publicServer.Close()
			internalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				internalHits++
				if r.URL.Path != "/test-bucket/photos/key.jpg" {
					t.Errorf("internal request path = %q", r.URL.Path)
				}
				w.Header().Set("ETag", `"etag"`)
			}))
			defer internalServer.Close()

			endpoint := publicServer.URL
			internalEndpoint := ""
			if tt.useInternal {
				internalEndpoint = internalServer.URL
			}
			if tt.trailingSlash {
				endpoint += "/"
				internalEndpoint += "/"
			}

			client, err := storage.NewClient(storage.Config{
				Endpoint:         endpoint,
				InternalEndpoint: internalEndpoint,
				Bucket:           "test-bucket",
				Region:           "us-east-1",
				AccessKeyID:      "test-access-key",
				SecretAccessKey:  "test-secret-key",
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			if _, err := client.Put(context.Background(), "photos/key.jpg", strings.NewReader("data"), 4, "text/plain"); err != nil {
				t.Fatalf("Put() error = %v", err)
			}
			if got := hitsForEndpoint(tt.wantBackendHit, publicHits, internalHits); got != 1 {
				t.Errorf("%s endpoint hits = %d, want 1", tt.wantBackendHit, got)
			}

			presign, err := client.PresignGet(context.Background(), "photos/key.jpg", time.Minute)
			if err != nil {
				t.Fatalf("PresignGet() error = %v", err)
			}
			parsed, err := url.Parse(presign.URL)
			if err != nil {
				t.Fatalf("parse presigned URL: %v", err)
			}
			if parsed.Host != strings.TrimPrefix(publicServer.URL, "http://") {
				t.Errorf("presigned host = %q, want public endpoint", parsed.Host)
			}
		})
	}
}

func TestNewClient_ConfiguresSDKClients(t *testing.T) {
	var authorization, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		path = r.URL.Path
		w.Header().Set("ETag", `"etag"`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{
		Endpoint:        server.URL,
		Bucket:          "test-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// Constructing a client must not contact the endpoint.
	if authorization != "" {
		t.Fatal("NewClient() contacted the storage endpoint")
	}

	if _, err := client.Put(context.Background(), "photos/key.jpg", strings.NewReader("data"), 4, "text/plain"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if path != "/test-bucket/photos/key.jpg" {
		t.Errorf("path = %q, want path-style bucket URL", path)
	}
	if !strings.Contains(authorization, "Credential=test-access-key/") ||
		!strings.Contains(authorization, "/us-east-1/s3/aws4_request") {
		t.Errorf("authorization header does not contain the configured credentials and region: %q", authorization)
	}
}

func hitsForEndpoint(endpoint string, publicHits, internalHits int) int {
	if endpoint == "internal" {
		return internalHits
	}
	return publicHits
}
