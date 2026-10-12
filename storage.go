package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// storageConfig holds the RustFS (S3-compatible object storage) settings read
// from the environment.
type storageConfig struct {
	endpoint  string
	accessKey string
	secretKey string
	region    string
	bucket    string
	secure    bool
}

// storageConfigFromEnv reads the RustFS settings. The second return value is
// false when RUSTFS_ENDPOINT is unset, which leaves object storage disabled.
func storageConfigFromEnv() (storageConfig, bool, error) {
	raw := strings.TrimSpace(os.Getenv("RUSTFS_ENDPOINT"))
	if raw == "" {
		return storageConfig{}, false, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return storageConfig{}, false, fmt.Errorf("parse RUSTFS_ENDPOINT %q: %w", raw, err)
	}
	if u.Host == "" {
		return storageConfig{}, false, fmt.Errorf("RUSTFS_ENDPOINT %q has no host", raw)
	}
	cfg := storageConfig{
		endpoint:  u.Host,
		accessKey: os.Getenv("RUSTFS_ACCESS_KEY"),
		secretKey: os.Getenv("RUSTFS_SECRET_KEY"),
		region:    envString("RUSTFS_REGION", "us-east-1"),
		bucket:    envString("RUSTFS_BUCKET", "clinic"),
		secure:    u.Scheme == "https",
	}
	if cfg.accessKey == "" || cfg.secretKey == "" {
		return storageConfig{}, false, fmt.Errorf("RUSTFS_ENDPOINT is set but RUSTFS_ACCESS_KEY or RUSTFS_SECRET_KEY is missing")
	}
	return cfg, true, nil
}

// objectStore wraps the S3 client and the bucket the backend stores objects in.
// It is kept concrete; consumers live in this package for now.
type objectStore struct {
	client *minio.Client
	bucket string
}

// newObjectStore connects to RustFS and ensures the configured bucket exists.
// RustFS uses path-style addressing, which minio-go selects by default for
// non-AWS endpoints.
func newObjectStore(ctx context.Context, cfg storageConfig) (*objectStore, error) {
	client, err := minio.New(cfg.endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.accessKey, cfg.secretKey, ""),
		Secure: cfg.secure,
		Region: cfg.region,
	})
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}
	exists, err := client.BucketExists(ctx, cfg.bucket)
	if err != nil {
		return nil, fmt.Errorf("check storage bucket %q: %w", cfg.bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.bucket, minio.MakeBucketOptions{Region: cfg.region}); err != nil {
			return nil, fmt.Errorf("create storage bucket %q: %w", cfg.bucket, err)
		}
	}
	return &objectStore{client: client, bucket: cfg.bucket}, nil
}

// healthHandler reports whether the storage backend and its bucket are
// reachable. It is registered on /healthz/storage so the connection is
// exercised after startup as well. A nil store means storage is disabled.
func (s *objectStore) healthHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "storage": "disabled"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		exists, err := s.client.BucketExists(ctx, s.bucket)
		if err != nil || !exists {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "storage": "down"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "storage": "up"})
	}
}
