package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"clinic-backend/internal/storage"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type capturedRequest struct {
	method        string
	path          string
	contentType   string
	contentLength int64
	body          []byte
}

func newObjectTestClient(t *testing.T, endpoint string) *storage.Client {
	t.Helper()

	client, err := storage.NewClient(storage.Config{
		Endpoint:        endpoint,
		Bucket:          "test-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	if err != nil {
		t.Fatalf("storage.NewClient() error = %v", err)
	}
	return client
}

func TestClient_Put(t *testing.T) {
	key := "temporary/key with space.txt"
	content := []byte("uploaded body")
	requests := make(chan capturedRequest, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		requests <- capturedRequest{
			method:        r.Method,
			path:          r.URL.Path,
			contentType:   r.Header.Get("Content-Type"),
			contentLength: r.ContentLength,
			body:          body,
		}
		w.Header().Set("ETag", `"opaque-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newObjectTestClient(t, server.URL)
	etag, err := client.Put(context.Background(), key, bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if etag != `"opaque-etag"` {
		t.Fatalf("ETag = %q, want %q", etag, `"opaque-etag"`)
	}

	got := <-requests
	if got.method != http.MethodPut {
		t.Errorf("method = %q, want %q", got.method, http.MethodPut)
	}
	if got.path != "/test-bucket/"+key {
		t.Errorf("path = %q, want %q", got.path, "/test-bucket/"+key)
	}
	if got.contentType != "text/plain" {
		t.Errorf("content type = %q, want %q", got.contentType, "text/plain")
	}
	if got.contentLength != int64(len(content)) {
		t.Errorf("content length = %d, want %d", got.contentLength, len(content))
	}
	if !bytes.Equal(got.body, content) {
		t.Errorf("body = %q, want %q", got.body, content)
	}
}

func TestClient_Get(t *testing.T) {
	key := "photos/streamed image.jpg"
	content := []byte("streamed body")
	requests := make(chan capturedRequest, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{method: r.Method, path: r.URL.Path}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Header().Set("ETag", `"get-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	client := newObjectTestClient(t, server.URL)
	object, err := client.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer object.Body.Close()

	if object.Metadata.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", object.Metadata.Size, len(content))
	}
	if object.Metadata.ContentType != "image/jpeg" {
		t.Errorf("content type = %q, want %q", object.Metadata.ContentType, "image/jpeg")
	}
	if object.Metadata.ETag != `"get-etag"` {
		t.Errorf("ETag = %q, want %q", object.Metadata.ETag, `"get-etag"`)
	}

	gotBody, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatalf("read object body: %v", err)
	}
	if !bytes.Equal(gotBody, content) {
		t.Errorf("body = %q, want %q", gotBody, content)
	}

	got := <-requests
	if got.method != http.MethodGet {
		t.Errorf("method = %q, want %q", got.method, http.MethodGet)
	}
	if got.path != "/test-bucket/"+key {
		t.Errorf("path = %q, want %q", got.path, "/test-bucket/"+key)
	}
}

func TestClient_Head(t *testing.T) {
	key := "photos/head.jpg"
	requests := make(chan capturedRequest, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{method: r.Method, path: r.URL.Path}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "1234")
		w.Header().Set("ETag", `"head-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newObjectTestClient(t, server.URL)
	metadata, err := client.Head(context.Background(), key)
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if metadata.Size != 1234 {
		t.Errorf("size = %d, want 1234", metadata.Size)
	}
	if metadata.ContentType != "image/png" {
		t.Errorf("content type = %q, want %q", metadata.ContentType, "image/png")
	}
	if metadata.ETag != `"head-etag"` {
		t.Errorf("ETag = %q, want %q", metadata.ETag, `"head-etag"`)
	}

	got := <-requests
	if got.method != http.MethodHead {
		t.Errorf("method = %q, want %q", got.method, http.MethodHead)
	}
	if got.path != "/test-bucket/"+key {
		t.Errorf("path = %q, want %q", got.path, "/test-bucket/"+key)
	}
}

func TestClient_Delete(t *testing.T) {
	key := "temporary/delete.txt"
	requests := make(chan capturedRequest, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{method: r.Method, path: r.URL.Path}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newObjectTestClient(t, server.URL)
	if err := client.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	got := <-requests
	if got.method != http.MethodDelete {
		t.Errorf("method = %q, want %q", got.method, http.MethodDelete)
	}
	if got.path != "/test-bucket/"+key {
		t.Errorf("path = %q, want %q", got.path, "/test-bucket/"+key)
	}
}

func TestClient_Delete_AbsentSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newObjectTestClient(t, server.URL)
	if err := client.Delete(context.Background(), "temporary/missing.txt"); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}
}

func TestClient_NotFound(t *testing.T) {
	const noSuchKey = `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, noSuchKey)
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := newObjectTestClient(t, server.URL)

	if _, err := client.Get(context.Background(), "photos/missing.txt"); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("Get() error = %v, want ErrObjectNotFound", err)
	} else {
		assertSDKError(t, err)
	}

	if _, err := client.Head(context.Background(), "photos/missing.txt"); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("Head() error = %v, want ErrObjectNotFound", err)
	} else {
		assertSDKError(t, err)
	}
}

func TestClient_Delete_PreservesSDKError(t *testing.T) {
	const invalidRequest = `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>InvalidRequest</Code><Message>bad request</Message></Error>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, invalidRequest)
	}))
	defer server.Close()

	client := newObjectTestClient(t, server.URL)
	err := client.Delete(context.Background(), "temporary/bad.txt")
	if err == nil {
		t.Fatal("Delete() error = nil, want an SDK error")
	}
	assertSDKError(t, err)
	if !strings.Contains(err.Error(), "temporary/bad.txt") {
		t.Errorf("error = %q, want the object key", err)
	}
}

func TestClient_RejectsEmptyKey(t *testing.T) {
	client := newObjectTestClient(t, "http://127.0.0.1:1")
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "put",
			call: func() error {
				_, err := client.Put(ctx, "", strings.NewReader("body"), 4, "text/plain")
				return err
			},
		},
		{
			name: "get",
			call: func() error {
				_, err := client.Get(ctx, "")
				return err
			},
		},
		{
			name: "head",
			call: func() error {
				_, err := client.Head(ctx, "")
				return err
			},
		},
		{
			name: "delete",
			call: func() error {
				return client.Delete(ctx, "")
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

func TestClient_RespectsCanceledContext(t *testing.T) {
	client := newObjectTestClient(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "put",
			call: func() error {
				_, err := client.Put(ctx, "temporary/canceled.txt", strings.NewReader("body"), 4, "text/plain")
				return err
			},
		},
		{
			name: "get",
			call: func() error {
				_, err := client.Get(ctx, "temporary/canceled.txt")
				return err
			},
		},
		{
			name: "head",
			call: func() error {
				_, err := client.Head(ctx, "temporary/canceled.txt")
				return err
			},
		},
		{
			name: "delete",
			call: func() error {
				return client.Delete(ctx, "temporary/canceled.txt")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}
}

func assertSDKError(t *testing.T, err error) {
	t.Helper()

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return
	}

	var responseErr *smithyhttp.ResponseError
	if errors.As(err, &responseErr) {
		return
	}

	t.Fatalf("expected a wrapped SDK error, got %T: %v", err, err)
}
