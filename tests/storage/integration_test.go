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
	"net/http"
	"net/url"
	"os"
	"strings"
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

func TestRustFSIntegration_PresignedURLs(t *testing.T) {
	client := integrationClient(t)
	key := uniqueIntegrationKey(t)
	content := []byte("stage 3 presigned integration\n")
	contentType := "text/plain"

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	if err := client.Check(ctx); err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Delete(cleanupCtx, key); err != nil {
			t.Logf("cleanup delete %q: %v", key, err)
		}
	})

	put, err := client.PresignPut(ctx, key, contentType, 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut() error = %v", err)
	}
	if put.Method != http.MethodPut {
		t.Fatalf("PresignPut() method = %q, want %q", put.Method, http.MethodPut)
	}
	if put.ExpiresAt.IsZero() {
		t.Fatal("PresignPut() returned a zero ExpiresAt")
	}

	uploadResponse := doPresignedRequest(t, newPresignedRequest(t, ctx, put, content))
	if uploadResponse.StatusCode < http.StatusOK || uploadResponse.StatusCode >= http.StatusMultipleChoices {
		uploadResponse.Body.Close()
		t.Fatalf("presigned PUT status = %d, want 2xx", uploadResponse.StatusCode)
	}
	uploadResponse.Body.Close()

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

	get, err := client.PresignGet(ctx, key, 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	if get.Method != http.MethodGet {
		t.Fatalf("PresignGet() method = %q, want %q", get.Method, http.MethodGet)
	}

	downloadResponse := doPresignedRequest(t, newPresignedRequest(t, ctx, get, nil))
	if downloadResponse.StatusCode != http.StatusOK {
		downloadResponse.Body.Close()
		t.Fatalf("presigned GET status = %d, want 200", downloadResponse.StatusCode)
	}
	got, readErr := io.ReadAll(downloadResponse.Body)
	closeErr := downloadResponse.Body.Close()
	if readErr != nil {
		t.Fatalf("read presigned GET body: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close presigned GET body: %v", closeErr)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("presigned GET body = %q, want %q", got, content)
	}

	unsignedURL := objectURLWithoutQuery(t, put.URL)
	unsignedResponse := doPresignedRequest(t, newPresignedRequest(t, ctx, storage.SignedRequest{
		Method: http.MethodGet,
		URL:    unsignedURL,
	}, nil))
	if unsignedResponse.StatusCode != http.StatusForbidden {
		unsignedResponse.Body.Close()
		t.Fatalf("unsigned GET status = %d, want 403", unsignedResponse.StatusCode)
	}
	unsignedResponse.Body.Close()

	tamperedResponse := doPresignedRequest(t, newPresignedRequest(t, ctx, storage.SignedRequest{
		Method: http.MethodGet,
		URL:    tamperSignature(t, get.URL),
	}, nil))
	if tamperedResponse.StatusCode != http.StatusForbidden {
		tamperedResponse.Body.Close()
		t.Fatalf("tampered GET status = %d, want 403", tamperedResponse.StatusCode)
	}
	tamperedResponse.Body.Close()

	shortLifetime := time.Second
	expired, err := client.PresignGet(ctx, key, shortLifetime)
	if err != nil {
		t.Fatalf("PresignGet() with short expiry error = %v", err)
	}
	time.Sleep(shortLifetime + 2*time.Second)

	expiredResponse := doPresignedRequest(t, newPresignedRequest(t, ctx, expired, nil))
	if expiredResponse.StatusCode >= http.StatusOK && expiredResponse.StatusCode < http.StatusMultipleChoices {
		expiredResponse.Body.Close()
		t.Fatalf("expired presigned GET status = %d, want a failure", expiredResponse.StatusCode)
	}
	expiredResponse.Body.Close()

	if err := client.Delete(ctx, key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func newPresignedRequest(t *testing.T, ctx context.Context, signed storage.SignedRequest, body []byte) *http.Request {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	request, err := http.NewRequestWithContext(ctx, signed.Method, signed.URL, reader)
	if err != nil {
		t.Fatalf("create presigned %s request", signed.Method)
	}
	if body != nil {
		request.ContentLength = int64(len(body))
	}

	for name, values := range signed.RequiredHeaders {
		if strings.EqualFold(name, "Host") {
			if len(values) > 0 {
				request.Host = values[0]
			}
			continue
		}
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}

	return request
}

func doPresignedRequest(t *testing.T, request *http.Request) *http.Response {
	t.Helper()

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("presigned %s request failed", request.Method)
	}
	return response
}

func objectURLWithoutQuery(t *testing.T, rawURL string) string {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal("parse presigned URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func tamperSignature(t *testing.T, rawURL string) string {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal("parse presigned URL")
	}

	query := parsed.Query()
	signature := query.Get("X-Amz-Signature")
	if signature == "" {
		t.Fatal("presigned URL has no signature")
	}

	replacement := byte('0')
	if signature[len(signature)-1] == '0' {
		replacement = '1'
	}
	query.Set("X-Amz-Signature", signature[:len(signature)-1]+string(replacement))
	parsed.RawQuery = query.Encode()
	return parsed.String()
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
