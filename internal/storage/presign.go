package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// SignedRequest describes a presigned object request.
type SignedRequest struct {
	URL             string
	Method          string
	RequiredHeaders http.Header
	ExpiresAt       time.Time
}

// PresignPut returns a signed upload request for key.
func (c *Client) PresignPut(ctx context.Context, key, contentType string, expiry time.Duration) (SignedRequest, error) {
	if err := validateKey(key); err != nil {
		return SignedRequest{}, err
	}
	if expiry <= 0 {
		return SignedRequest{}, errors.New("storage: expiry must be positive")
	}

	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return SignedRequest{}, errors.New("storage: content type is required")
	}

	presigned, err := s3.NewPresignClient(c.presign).PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, func(options *s3.PresignOptions) {
		options.Expires = expiry
	})
	if err != nil {
		return SignedRequest{}, fmt.Errorf("storage: presign put object %q: %w", key, err)
	}

	return signedRequest(presigned.URL, presigned.Method, presigned.SignedHeader, time.Now().UTC().Add(expiry)), nil
}

// PresignGet returns a signed download request for key.
func (c *Client) PresignGet(ctx context.Context, key string, expiry time.Duration) (SignedRequest, error) {
	if err := validateKey(key); err != nil {
		return SignedRequest{}, err
	}
	if expiry <= 0 {
		return SignedRequest{}, errors.New("storage: expiry must be positive")
	}

	presigned, err := s3.NewPresignClient(c.presign).PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, func(options *s3.PresignOptions) {
		options.Expires = expiry
	})
	if err != nil {
		return SignedRequest{}, fmt.Errorf("storage: presign get object %q: %w", key, err)
	}

	return signedRequest(presigned.URL, presigned.Method, presigned.SignedHeader, time.Now().UTC().Add(expiry)), nil
}

func signedRequest(url, method string, headers http.Header, expiresAt time.Time) SignedRequest {
	requiredHeaders := make(http.Header, len(headers))
	for name, values := range headers {
		requiredHeaders[name] = append([]string(nil), values...)
	}

	return SignedRequest{
		URL:             url,
		Method:          method,
		RequiredHeaders: requiredHeaders,
		ExpiresAt:       expiresAt,
	}
}
