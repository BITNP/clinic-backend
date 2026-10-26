package storage_test

// Run the opt-in RustFS integration test with dedicated test credentials.
// The bucket must already exist, and the test credentials must be allowed to
// use the temporary/ prefix.
//
//	STORAGE_TEST_ENDPOINT=http://127.0.0.1:9000 \
//	STORAGE_TEST_BUCKET=clinic-photos \
//	STORAGE_TEST_REGION=us-east-1 \
//	STORAGE_TEST_ACCESS_KEY_ID=... \
//	STORAGE_TEST_SECRET_ACCESS_KEY=... \
//	go test ./tests/storage -run TestRustFSIntegration -v

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"clinic-backend/internal/storage"
)

func TestRustFSIntegration_ObjectLifecycle(t *testing.T) {
	client := integrationClient(t)
	key := uniqueIntegrationKey(t)
	content := []byte("stage 2 storage integration\n")
	contentType := "text/plain"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Delete(cleanupCtx, key); err != nil {
			t.Logf("cleanup delete %q: %v", key, err)
		}
	})

	etag, err := client.Put(ctx, key, bytes.NewReader(content), int64(len(content)), contentType)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if etag == "" {
		t.Fatal("Put() returned an empty ETag")
	}

	head, err := client.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head.Size != int64(len(content)) {
		t.Errorf("Head() size = %d, want %d", head.Size, len(content))
	}
	if head.ContentType != contentType {
		t.Errorf("Head() content type = %q, want %q", head.ContentType, contentType)
	}
	if head.ETag == "" {
		t.Error("Head() returned an empty ETag")
	}

	object, err := client.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	got, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil {
		t.Fatalf("read object body: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close object body: %v", closeErr)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Get() body = %q, want %q", got, content)
	}
	if object.Metadata.Size != int64(len(content)) {
		t.Errorf("Get() size = %d, want %d", object.Metadata.Size, len(content))
	}
	if object.Metadata.ContentType != contentType {
		t.Errorf("Get() content type = %q, want %q", object.Metadata.ContentType, contentType)
	}
	if object.Metadata.ETag == "" {
		t.Error("Get() returned an empty ETag")
	}

	if err := client.Delete(ctx, key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := client.Get(ctx, key); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("Get() after delete error = %v, want ErrObjectNotFound", err)
	}
	if _, err := client.Head(ctx, key); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("Head() after delete error = %v, want ErrObjectNotFound", err)
	}
	if err := client.Delete(ctx, key); err != nil {
		t.Fatalf("Delete() of absent object error = %v, want nil", err)
	}

	if _, err := client.Put(ctx, "", bytes.NewReader(content), int64(len(content)), contentType); err == nil {
		t.Fatal("Put() with an empty key returned nil error")
	}

	canceledCtx, cancelCanceled := context.WithCancel(context.Background())
	cancelCanceled()
	if _, err := client.Head(canceledCtx, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("Head() with canceled context error = %v, want context.Canceled", err)
	}
}

func integrationClient(t *testing.T) *storage.Client {
	t.Helper()

	endpoint := os.Getenv("STORAGE_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set STORAGE_TEST_ENDPOINT, STORAGE_TEST_BUCKET, STORAGE_TEST_REGION, STORAGE_TEST_ACCESS_KEY_ID, and STORAGE_TEST_SECRET_ACCESS_KEY to run the RustFS integration test")
	}

	client, err := storage.NewClient(storage.Config{
		Endpoint:        endpoint,
		Bucket:          os.Getenv("STORAGE_TEST_BUCKET"),
		Region:          os.Getenv("STORAGE_TEST_REGION"),
		AccessKeyID:     os.Getenv("STORAGE_TEST_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("STORAGE_TEST_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		t.Fatalf("storage.NewClient() error = %v", err)
	}

	return client
}

func uniqueIntegrationKey(t *testing.T) string {
	t.Helper()

	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate object key: %v", err)
	}

	return "temporary/storage-integration-" + hex.EncodeToString(random[:]) + ".txt"
}
