package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// Service is the application-facing storage handle. It owns the configured
// client and the URL expiry defaults.
type Service struct {
	client    *Client
	putURLTTL time.Duration
	getURLTTL time.Duration
}

// NewService creates a storage service. The client is required.
func NewService(client *Client, putURLTTL, getURLTTL time.Duration) (*Service, error) {
	if client == nil {
		return nil, errors.New("storage: client is required")
	}
	if putURLTTL <= 0 {
		return nil, errors.New("storage: put URL expiry must be positive")
	}
	if getURLTTL <= 0 {
		return nil, errors.New("storage: get URL expiry must be positive")
	}

	return &Service{
		client:    client,
		putURLTTL: putURLTTL,
		getURLTTL: getURLTTL,
	}, nil
}

// Check verifies that the configured bucket is accessible.
func (s *Service) Check(ctx context.Context) error {
	return s.client.Check(ctx)
}

// Put uploads body to key and returns the object ETag.
func (s *Service) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) (string, error) {
	return s.client.Put(ctx, key, body, size, contentType)
}

// Get opens key for streaming. The caller must close the returned body.
func (s *Service) Get(ctx context.Context, key string) (*Object, error) {
	return s.client.Get(ctx, key)
}

// Head returns object metadata without downloading the object.
func (s *Service) Head(ctx context.Context, key string) (ObjectMetadata, error) {
	return s.client.Head(ctx, key)
}

// Delete removes key. Deleting an absent object succeeds.
func (s *Service) Delete(ctx context.Context, key string) error {
	return s.client.Delete(ctx, key)
}

// PresignPut returns a signed upload request using the configured put expiry.
func (s *Service) PresignPut(ctx context.Context, key, contentType string) (SignedRequest, error) {
	return s.client.PresignPut(ctx, key, contentType, s.putURLTTL)
}

// PresignGet returns a signed download request using the configured get expiry.
func (s *Service) PresignGet(ctx context.Context, key string) (SignedRequest, error) {
	return s.client.PresignGet(ctx, key, s.getURLTTL)
}
