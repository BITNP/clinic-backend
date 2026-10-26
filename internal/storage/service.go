package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrStorageDisabled is returned when a storage operation is attempted while
// S3 storage is disabled.
var ErrStorageDisabled = errors.New("storage: disabled")

// Service is the application-facing storage handle. It owns the configured
// client and the URL expiry defaults. A disabled Service has no client and
// returns ErrStorageDisabled from every operation.
type Service struct {
	client    *Client
	putURLTTL time.Duration
	getURLTTL time.Duration
}

// NewService creates a storage service. A nil client creates a disabled
// service; all operations then return ErrStorageDisabled.
func NewService(client *Client, putURLTTL, getURLTTL time.Duration) (*Service, error) {
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

// Enabled reports whether storage operations are available.
func (s *Service) Enabled() bool {
	return s != nil && s.client != nil
}

// Put uploads body to key and returns the object ETag.
func (s *Service) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) (string, error) {
	client, err := s.clientOrError()
	if err != nil {
		return "", err
	}
	return client.Put(ctx, key, body, size, contentType)
}

// Get opens key for streaming. The caller must close the returned body.
func (s *Service) Get(ctx context.Context, key string) (*Object, error) {
	client, err := s.clientOrError()
	if err != nil {
		return nil, err
	}
	return client.Get(ctx, key)
}

// Head returns object metadata without downloading the object.
func (s *Service) Head(ctx context.Context, key string) (ObjectMetadata, error) {
	client, err := s.clientOrError()
	if err != nil {
		return ObjectMetadata{}, err
	}
	return client.Head(ctx, key)
}

// Delete removes key. Deleting an absent object succeeds.
func (s *Service) Delete(ctx context.Context, key string) error {
	client, err := s.clientOrError()
	if err != nil {
		return err
	}
	return client.Delete(ctx, key)
}

// PresignPut returns a signed upload request using the configured put expiry.
func (s *Service) PresignPut(ctx context.Context, key, contentType string) (SignedRequest, error) {
	client, err := s.clientOrError()
	if err != nil {
		return SignedRequest{}, err
	}
	return client.PresignPut(ctx, key, contentType, s.putURLTTL)
}

// PresignGet returns a signed download request using the configured get expiry.
func (s *Service) PresignGet(ctx context.Context, key string) (SignedRequest, error) {
	client, err := s.clientOrError()
	if err != nil {
		return SignedRequest{}, err
	}
	return client.PresignGet(ctx, key, s.getURLTTL)
}

func (s *Service) clientOrError() (*Client, error) {
	if s == nil || s.client == nil {
		return nil, ErrStorageDisabled
	}
	return s.client, nil
}
